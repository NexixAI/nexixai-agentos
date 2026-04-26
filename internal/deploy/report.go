package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CheckResult struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
	URL       string `json:"url,omitempty"`
	LatencyMs int    `json:"latency_ms,omitempty"`
}

type Summary struct {
	Timestamp string            `json:"timestamp"`
	Mode      string            `json:"mode"`
	Endpoints map[string]string `json:"endpoints"`
	Checks    []CheckResult     `json:"checks"`
}

func WriteSummary(dir string, s Summary) error {
	s.Timestamp = time.Now().UTC().Format(time.RFC3339)

	// JSON
	jb, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), jb, 0o644); err != nil {
		return err
	}

	// Markdown
	var md strings.Builder
	md.WriteString("# AgentOS Deployment Summary\n\n")
	md.WriteString(fmt.Sprintf("- Timestamp: `%s`\n", s.Timestamp))
	md.WriteString(fmt.Sprintf("- Mode: `%s`\n\n", s.Mode))

	md.WriteString("## Endpoints\n")
	for k, v := range s.Endpoints {
		md.WriteString(fmt.Sprintf("- **%s**: %s\n", k, v))
	}

	md.WriteString("\n## Checks\n")
	for _, c := range s.Checks {
		status := "ok"
		if !c.OK {
			status = "fail"
		}
		md.WriteString(fmt.Sprintf("- %s **%s** (%s) - %s (%dms)\n", status, c.Name, c.URL, c.Detail, c.LatencyMs))
	}

	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(md.String()), 0o644)
}
