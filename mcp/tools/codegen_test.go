package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractCodeBlocks(t *testing.T) {
	content := "Here's my approach:\n\n" +
		"```internal/governance/types.go\n" +
		"package governance\n\ntype Config struct{}\n" +
		"```\n\n" +
		"And the engine:\n\n" +
		"```internal/governance/engine.go\n" +
		"package governance\n\nfunc NewEngine() *Engine { return nil }\n" +
		"```\n"

	files := extractCodeBlocks(content)

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Path != "internal/governance/types.go" {
		t.Errorf("expected types.go, got %q", files[0].Path)
	}
	if files[1].Path != "internal/governance/engine.go" {
		t.Errorf("expected engine.go, got %q", files[1].Path)
	}
	if files[0].Content != "package governance\n\ntype Config struct{}" {
		t.Errorf("unexpected content: %q", files[0].Content)
	}
}

func TestExtractCodeBlocks_SkipsLanguageOnlyBlocks(t *testing.T) {
	content := "```json\n{\"key\": \"value\"}\n```\n\n" +
		"```src/main.go\npackage main\n```\n"

	files := extractCodeBlocks(content)

	if len(files) != 1 {
		t.Fatalf("expected 1 file (skipping json), got %d", len(files))
	}
	if files[0].Path != "src/main.go" {
		t.Errorf("expected src/main.go, got %q", files[0].Path)
	}
}

func TestExtractCodeBlocks_Empty(t *testing.T) {
	files := extractCodeBlocks("No code blocks here.")
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestExtractReasoning(t *testing.T) {
	content := "I'll modify the engine to add rate limiting.\n\n" +
		"```engine.go\npackage main\n```\n\n" +
		"This should handle the requirements."

	reasoning := extractReasoning(content)

	if !strings.Contains(reasoning, "modify the engine") || !strings.Contains(reasoning, "handle the requirements") {
		t.Errorf("unexpected reasoning: %q", reasoning)
	}
}

func TestBuildCodegenPrompt(t *testing.T) {
	input := codegenInput{
		Repo:       "nexixai/nexixai-agentos",
		IssueTitle: "Add rate limit",
		IssueBody:  "## Criteria\n- Split observe/action",
		FileScope:  []string{"engine.go"},
		Context:    "From plan.md: issue #1",
	}

	prompt := buildCodegenPrompt(input, "### engine.go\n```\npackage main\n```\n")

	for _, want := range []string{"Add rate limit", "Split observe/action", "From plan.md", "engine.go"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestExtractSearchReplaceBlocks_Simple(t *testing.T) {
	tmp := t.TempDir()
	repoFile := filepath.Join(tmp, "test.go")
	original := `package main

func add(a, b int) int {
	return a + b
}

func main() {
	add(1, 2)
}
`
	if err := os.WriteFile(repoFile, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	llmOutput := "Here's my approach:\n\n" +
		"### file: test.go\n" +
		"SEARCH:\n" +
		"```\n" +
		"func add(a, b int) int {\n\treturn a + b\n}\n" +
		"```\n" +
		"REPLACE:\n" +
		"```\n" +
		"func add(a, b int) int {\n\treturn a + b\n}\n\nfunc sub(a, b int) int {\n\treturn a - b\n}\n" +
		"```\n"

	files, err := extractSearchReplaceBlocks(llmOutput, tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !strings.Contains(files[0].Content, "func sub") {
		t.Error("expected sub function in result")
	}
	if !strings.Contains(files[0].Content, "func add") {
		t.Error("expected add function preserved in result")
	}
	if !strings.Contains(files[0].Content, "func main") {
		t.Error("expected main function preserved in result")
	}
}

func TestExtractSearchReplaceBlocks_Hallucination(t *testing.T) {
	tmp := t.TempDir()
	repoFile := filepath.Join(tmp, "test.go")
	if err := os.WriteFile(repoFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	llmOutput := "### file: test.go\n" +
		"SEARCH:\n" +
		"```\n" +
		"this code does not exist in the file\n" +
		"```\n" +
		"REPLACE:\n" +
		"```\n" +
		"new code\n" +
		"```\n"

	_, err := extractSearchReplaceBlocks(llmOutput, tmp)
	if err == nil {
		t.Fatal("expected error for hallucinated SEARCH text")
	}
	if !strings.Contains(err.Error(), "hallucinated") {
		t.Errorf("expected hallucination error, got: %v", err)
	}
}

func TestExtractSearchReplaceBlocks_NoBlocks(t *testing.T) {
	files, err := extractSearchReplaceBlocks("Just some text, no blocks here.", "/nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestExtractSearchReplaceBlocks_MultipleEditsPerFile(t *testing.T) {
	tmp := t.TempDir()
	repoFile := filepath.Join(tmp, "test.go")
	original := `package main

const A = 1
const B = 2
const C = 3
`
	if err := os.WriteFile(repoFile, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	llmOutput := "### file: test.go\n" +
		"SEARCH:\n```\nconst A = 1\n```\nREPLACE:\n```\nconst A = 10\n```\n\n" +
		"### file: test.go\n" +
		"SEARCH:\n```\nconst C = 3\n```\nREPLACE:\n```\nconst C = 30\n```\n"

	files, err := extractSearchReplaceBlocks(llmOutput, tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !strings.Contains(files[0].Content, "const A = 10") {
		t.Error("expected first edit applied")
	}
	if !strings.Contains(files[0].Content, "const C = 30") {
		t.Error("expected second edit applied")
	}
	if !strings.Contains(files[0].Content, "const B = 2") {
		t.Error("expected unchanged code preserved")
	}
}
