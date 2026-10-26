package wsl

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRevisionProtectsExternalEditsAndExistence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	path := filepath.Join(dir, ".wslconfig")
	service := &Service{}
	initial, err := readConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	params := ConfigWriteParams{Target: "wslconfig", Content: "[wsl2]\nmemory=4GB\n", ExpectedRevision: initial["revision"].(string)}
	// An externally-created empty file is distinct from a missing file.
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfigWrite(context.Background(), params); err == nil || err.Code != "CONFLICT" {
		t.Fatalf("expected conflict: %v", err)
	}
	current, _ := readConfigFile(path)
	params.ExpectedRevision = current["revision"].(string)
	saved, err := service.ConfigWrite(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	backup := saved.(map[string]any)["backupPath"].(string)
	if raw, err := os.ReadFile(backup); err != nil || len(raw) != 0 {
		t.Fatalf("backup: %q %v", raw, err)
	}
	if _, err := service.ConfigWrite(context.Background(), params); err == nil || err.Code != "CONFLICT" {
		t.Fatalf("stale write accepted: %v", err)
	}
	params.ExpectedRevision = ""
	if _, err := service.ConfigWrite(context.Background(), params); err == nil || err.Code != "INVALID_PARAMS" {
		t.Fatalf("unversioned write accepted: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != params.Content {
		t.Fatal("conflicting write changed file")
	}
}
