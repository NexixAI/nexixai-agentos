package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/config"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/mcp"
)

// CodegenConfig holds dependencies for the generate_implementation tool.
type CodegenConfig struct {
	ModelConfig config.ModelConfig
	RepoBase    string // base path where repos are checked out (e.g., /workspace in container, mounted from host-side clones)
	HTTPClient  *http.Client
}

func (c *CodegenConfig) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// codegenInput is the JSON schema input for generate_implementation.
type codegenInput struct {
	Repo       string   `json:"repo"`
	IssueTitle string   `json:"issue_title"`
	IssueBody  string   `json:"issue_body"`
	FileScope  []string `json:"file_scope"`
	Context    string   `json:"context,omitempty"`
}

// codegenFileResult is a single file in the generation output.
type codegenFileResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Action  string `json:"action"` // "create" or "modify"
}

// codegenResult is the structured output of generate_implementation.
type codegenResult struct {
	Files      []codegenFileResult `json:"files"`
	Reasoning  string              `json:"reasoning"`
	TokensUsed int                 `json:"tokens_used"`
}

var codegenInputSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"repo": {
			"type": "string",
			"description": "Repository name (e.g., nexixai/nexixai-agentos)"
		},
		"issue_title": {
			"type": "string",
			"description": "Issue title describing what to implement"
		},
		"issue_body": {
			"type": "string",
			"description": "Issue body with acceptance criteria and context"
		},
		"file_scope": {
			"type": "array",
			"items": { "type": "string" },
			"description": "List of file paths to read and potentially modify"
		},
		"context": {
			"type": "string",
			"description": "Optional additional context (from KB, plan.md, etc.)"
		}
	},
	"required": ["repo", "issue_title", "issue_body", "file_scope"]
}`)

// RegisterCodegenTools registers the generate_implementation tool.
func RegisterCodegenTools(registry *mcp.ToolRegistry, cfg CodegenConfig) error {
	return registry.Register(mcp.Tool{
		Name:         "generate_implementation",
		Description:  "Generate code implementation using the local LLM. Takes an issue spec and file scope, returns file contents with diffs. Used by the Executor agent in the split Executor/Validator pipeline.",
		InputSchema:  codegenInputSchema,
		MinClearance: mcp.ClearanceExecute, // T2
		Static:       true,
		Category:     "action",
		Handler:      makeCodegenHandler(cfg),
	})
}

func makeCodegenHandler(cfg CodegenConfig) mcp.ToolHandler {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		start := time.Now()

		var input codegenInput
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if input.Repo == "" || input.IssueTitle == "" || len(input.FileScope) == 0 {
			return nil, fmt.Errorf("repo, issue_title, and file_scope are required")
		}

		// Resolve repo path
		repoName := input.Repo
		if idx := strings.LastIndex(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		repoPath := filepath.Join(cfg.RepoBase, repoName)

		// Read file contents and include line counts
		var fileContents strings.Builder
		for _, fp := range input.FileScope {
			fullPath := filepath.Join(repoPath, fp)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				fileContents.WriteString(fmt.Sprintf("### %s\n(file not found: %v)\n\n", fp, err))
				continue
			}
			lineCount := strings.Count(string(data), "\n") + 1
			fileContents.WriteString(fmt.Sprintf("### %s (%d lines)\n```\n%s\n```\n\n", fp, lineCount, string(data)))
		}

		// Build prompt
		prompt := buildCodegenPrompt(input, fileContents.String())

		// Call LLM
		reqBody := map[string]any{
			"model": cfg.ModelConfig.DefaultModel,
			"messages": []map[string]string{
				{"role": "system", "content": codegenSystemPrompt},
				{"role": "user", "content": prompt},
			},
			"stream":      false,
			"max_tokens":  32768,
			"temperature": 0.2,
		}

		payload, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}

		baseURL := strings.TrimRight(cfg.ModelConfig.BaseURL, "/")
		url := baseURL + "/chat/completions"
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if cfg.ModelConfig.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+cfg.ModelConfig.APIKey)
		}

		resp, err := cfg.httpClient().Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("LLM request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("LLM returned HTTP %d: %s", resp.StatusCode, string(body))
		}

		// Parse response
		var llmResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &llmResp); err != nil {
			return nil, fmt.Errorf("decode LLM response: %w", err)
		}

		if len(llmResp.Choices) == 0 {
			return nil, fmt.Errorf("LLM returned no choices")
		}

		content := llmResp.Choices[0].Message.Content
		tokensUsed := llmResp.Usage.TotalTokens

		// Try SEARCH/REPLACE format first (preferred — preserves files)
		// Falls back to whole-file fenced blocks if no SEARCH/REPLACE blocks found
		files, parseErr := extractSearchReplaceBlocks(content, repoPath)
		if parseErr != nil {
			slog.Warn("generate_implementation: SEARCH/REPLACE parse error",
				"error", parseErr,
				"repo", input.Repo)
		}
		if len(files) == 0 {
			files = extractCodeBlocks(content)
		}

		elapsed := time.Since(start)
		metrics.ObserveCodegenDuration(repoName, elapsed.Seconds())
		metrics.IncCodegenTotal(repoName, "ok")
		metrics.ObserveCodegenTokens(repoName, float64(tokensUsed))

		slog.Info("generate_implementation complete",
			"repo", input.Repo,
			"files", len(files),
			"tokens", tokensUsed,
			"duration_ms", elapsed.Milliseconds(),
		)

		// Extract reasoning (text outside code blocks)
		reasoning := extractReasoning(content)

		return codegenResult{
			Files:      files,
			Reasoning:  reasoning,
			TokensUsed: tokensUsed,
		}, nil
	}
}

const codegenSystemPrompt = `You are a code editor. Given an issue and existing file contents, output ONLY the minimal SEARCH/REPLACE blocks needed to satisfy the issue.

You MUST use SEARCH/REPLACE block format. Whole-file output is forbidden.

Format for each edit:

### file: path/to/file.go
SEARCH:
` + "```" + `
<exact existing code from the file — copy verbatim>
` + "```" + `
REPLACE:
` + "```" + `
<new code that should replace the search block>
` + "```" + `

Rules:
- The SEARCH block MUST be an EXACT character-for-character copy of code from the file. Whitespace, indentation, and punctuation must match.
- The SEARCH block must be unique within the file (long enough to identify a single location).
- Use multiple SEARCH/REPLACE blocks for the same file if you need to make multiple edits.
- To ADD new code (e.g., a new function), make the SEARCH block be the line BEFORE where you want to insert, and the REPLACE block be that same line PLUS your new code.
- Keep blocks small and surgical. Prefer 5-line blocks over 50-line blocks.
- NEVER output a whole file. NEVER use placeholder comments like "// existing code here".

Process:
1. Read the existing file carefully.
2. Identify the smallest exact section you need to change.
3. Copy that section verbatim into SEARCH.
4. Write the new version into REPLACE.
5. Repeat for each separate edit.

Example — adding a metric counter to an existing Go file:

### file: internal/metrics/metrics.go
SEARCH:
` + "```" + `
	memoryOps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_memory_operations_total",
			Help: "Memory store operations.",
		},
		[]string{"operation", "status"},
	)
)
` + "```" + `
REPLACE:
` + "```" + `
	memoryOps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentos_memory_operations_total",
			Help: "Memory store operations.",
		},
		[]string{"operation", "status"},
	)

	dailyTokenBudgetUsed = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agentos_daily_token_budget_used",
			Help: "Cumulative tokens used per agent per day.",
		},
		[]string{"agent"},
	)
)
` + "```" + `

Before the SEARCH/REPLACE blocks, write 1-2 sentences explaining your approach.`

func buildCodegenPrompt(input codegenInput, fileContents string) string {
	var sb strings.Builder

	sb.WriteString("## Issue\n\n")
	sb.WriteString("**Title:** " + input.IssueTitle + "\n\n")
	sb.WriteString(input.IssueBody + "\n\n")

	if input.Context != "" {
		sb.WriteString("## Additional Context\n\n")
		sb.WriteString(input.Context + "\n\n")
	}

	sb.WriteString("## Current File Contents\n\n")
	sb.WriteString(fileContents)

	sb.WriteString("\n## Instructions\n\n")
	sb.WriteString("Implement the changes described in the issue using SEARCH/REPLACE blocks ONLY.\n")
	sb.WriteString("Each block must contain exact existing code in SEARCH and the new code in REPLACE.\n")
	sb.WriteString("Do not output whole files. Do not use placeholder comments.\n")

	return sb.String()
}

// searchReplacePattern matches SEARCH/REPLACE blocks.
// Format:
//   ### file: path/to/file.ext
//   SEARCH:
//   ```
//   <text>
//   ```
//   REPLACE:
//   ```
//   <text>
//   ```
var searchReplacePattern = regexp.MustCompile("(?s)### file: ([^\\n]+)\\s*\\nSEARCH:\\s*\\n```[a-zA-Z]*\\s*\\n(.*?)\\n```\\s*\\nREPLACE:\\s*\\n```[a-zA-Z]*\\s*\\n(.*?)\\n```")

// extractSearchReplaceBlocks parses SEARCH/REPLACE blocks from LLM output and
// applies them to the original files. Returns the modified file contents.
//
// If a SEARCH block doesn't match the original file (LLM hallucinated), returns
// an error so the caller can retry or fail loudly.
func extractSearchReplaceBlocks(content string, repoPath string) ([]codegenFileResult, error) {
	matches := searchReplacePattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	// Group edits by file path
	type edit struct{ search, replace string }
	editsByFile := make(map[string][]edit)
	for _, m := range matches {
		if len(m) < 4 {
			continue
		}
		path := strings.TrimSpace(m[1])
		editsByFile[path] = append(editsByFile[path], edit{search: m[2], replace: m[3]})
	}

	results := []codegenFileResult{}
	for path, edits := range editsByFile {
		fullPath := filepath.Join(repoPath, path)
		original, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("file %s not found in repo: %w", path, err)
		}

		modified := string(original)
		for i, e := range edits {
			if !strings.Contains(modified, e.search) {
				return nil, fmt.Errorf("file %s edit %d: SEARCH text not found in file (LLM hallucinated)", path, i+1)
			}
			modified = strings.Replace(modified, e.search, e.replace, 1)
		}

		results = append(results, codegenFileResult{
			Path:    path,
			Content: modified,
			Action:  "modify",
		})
	}

	return results, nil
}

// codeBlockPattern matches fenced code blocks with an optional file path.
// Matches: ```path/to/file.go\n...\n```
var codeBlockPattern = regexp.MustCompile("(?s)```([^\\s`]+(?:\\.[a-zA-Z]+))\\s*\\n(.*?)\\n```")

func extractCodeBlocks(content string) []codegenFileResult {
	matches := codeBlockPattern.FindAllStringSubmatch(content, -1)
	results := []codegenFileResult{}

	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		path := strings.TrimSpace(m[1])
		code := m[2]

		// Skip non-file paths (e.g., "json", "yaml", "bash")
		if !strings.Contains(path, "/") && !strings.Contains(path, ".") {
			continue
		}

		results = append(results, codegenFileResult{
			Path:    path,
			Content: code,
			Action:  "modify",
		})
	}

	return results
}

func extractReasoning(content string) string {
	// Remove all code blocks, return the remaining text
	cleaned := codeBlockPattern.ReplaceAllString(content, "")
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) > 500 {
		cleaned = cleaned[:500]
	}
	return cleaned
}
