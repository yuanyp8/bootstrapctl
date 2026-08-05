package report

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveJSONAlsoWritesMarkdownSidecar(t *testing.T) {
	dir := t.TempDir()
	rep := New("verify", "demo", false)
	rep.Add(TaskResult{Node: "node-01", TaskKey: "host-facts", Title: "facts", Status: "ok", Summary: "observed"})

	jsonPath, err := rep.SaveJSON(dir)
	if err != nil {
		t.Fatalf("SaveJSON() error = %v", err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("JSON report missing: %v", err)
	}
	markdownPath := filepath.Join(dir, rep.RunID+".md")
	if _, err := os.Stat(markdownPath); err != nil {
		t.Fatalf("Markdown sidecar missing: %v", err)
	}
}
