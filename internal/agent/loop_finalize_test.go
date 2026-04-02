package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelocateToWorkspace_InsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	filePath := filepath.Join(ws, "test.html")
	if err := os.WriteFile(filePath, []byte("<html>test</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := relocateToWorkspace(ws, filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != filePath {
		t.Errorf("expected unchanged path %s, got %s", filePath, result)
	}
}

func TestRelocateToWorkspace_OutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "diagram.html")
	if err := os.WriteFile(tmpFile, []byte("<html>diagram</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := relocateToWorkspace(ws, tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, filepath.Join(ws, "generated")) {
		t.Errorf("expected path in workspace/generated, got %s", result)
	}

	content, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("failed to read relocated file: %v", err)
	}
	if string(content) != "<html>diagram</html>" {
		t.Errorf("expected copied content, got %s", string(content))
	}
}

func TestRelocateToWorkspace_EmptyWorkspace(t *testing.T) {
	result, err := relocateToWorkspace("", "/tmp/file.html")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "/tmp/file.html" {
		t.Errorf("expected original path, got %s", result)
	}
}

func TestRelocateToWorkspace_NameCollision(t *testing.T) {
	ws := t.TempDir()
	genDir := filepath.Join(ws, "generated")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "file.html"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "file.html")
	if err := os.WriteFile(tmpFile, []byte("new content"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := relocateToWorkspace(ws, tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == filepath.Join(genDir, "file.html") {
		t.Error("expected different name due to collision")
	}
	if !strings.Contains(filepath.Base(result), "file") {
		t.Errorf("expected stem 'file' in name, got %s", filepath.Base(result))
	}

	// Verify original file untouched
	orig, _ := os.ReadFile(filepath.Join(genDir, "file.html"))
	if string(orig) != "existing" {
		t.Error("original file was overwritten")
	}

	// Verify new file has correct content
	content, _ := os.ReadFile(result)
	if string(content) != "new content" {
		t.Errorf("expected 'new content', got %s", string(content))
	}
}
