package main

import "testing"

func TestVersionConstant(t *testing.T) {
	if version == "" {
		t.Error("version constant should not be empty")
	}
}

func TestDefaultComposeFile(t *testing.T) {
	f := defaultComposeFile()
	if f == "" {
		t.Error("defaultComposeFile should not return empty string")
	}
}
