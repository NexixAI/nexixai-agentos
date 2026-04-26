package deploy

import "testing"

func TestComposeRunnerDefaults(t *testing.T) {
	r := ComposeRunner{}
	if r.ComposeFile != "" {
		t.Errorf("ComposeFile should be zero value, got %q", r.ComposeFile)
	}
	if r.ProjectName != "" {
		t.Errorf("ProjectName should be zero value, got %q", r.ProjectName)
	}
	// Verify out/err don't panic with nil callbacks.
	r.out("test")
	r.err("test")
}

func TestComposeRunnerWithCallbacks(t *testing.T) {
	var gotOut, gotErr string
	r := ComposeRunner{
		ComposeFile: "compose.yaml",
		ProjectName: "test",
		Stdout:      func(s string) { gotOut = s },
		Stderr:      func(s string) { gotErr = s },
	}
	r.out("hello")
	r.err("world")
	if gotOut != "hello" {
		t.Errorf("Stdout callback got %q, want %q", gotOut, "hello")
	}
	if gotErr != "world" {
		t.Errorf("Stderr callback got %q, want %q", gotErr, "world")
	}
}
