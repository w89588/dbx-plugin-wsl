package wsl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceConfigPreviewApplyAndConflict(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	path := filepath.Join(dir, ".wslconfig")
	original := "\ufeff# preserved\r\n[wsl2]\r\nmemory=4GB # note\r\nnetworkingMode=mirrored\r\n[experimental]\r\nautoMemoryReclaim=gradual\r\n"
	os.WriteFile(path, []byte(original), 0600)
	s := &Service{}
	read, e := s.ResourceConfigRead()
	if e != nil {
		t.Fatal(e)
	}
	memory := int64(8192)
	swap := int64(0)
	p := ResourceConfigParams{ExpectedRevision: read.(map[string]any)["revision"].(string), MemoryMB: &memory, SwapMB: &swap}
	preview, e := s.ResourceConfigPreview(p)
	if e != nil {
		t.Fatal(e)
	}
	after := preview.(map[string]any)["after"].(string)
	for _, part := range []string{"\ufeff# preserved\r\n", "memory=8192MB # note", "networkingMode=mirrored", "autoMemoryReclaim=gradual", "swap=0MB"} {
		if !strings.Contains(after, part) {
			t.Fatal(after)
		}
	}
	before, _ := os.ReadFile(path)
	if string(before) != original {
		t.Fatal("preview modified file")
	}
	if _, e = s.ResourceConfigApply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResourceConfigApply(context.Background(), p); e == nil || e.Code != "CONFLICT" {
		t.Fatal("stale write accepted")
	}
	bad := int64(-1)
	if _, e = resourceConfig(after, ResourceConfigParams{MemoryMB: &bad}); e == nil {
		t.Fatal("invalid memory accepted")
	}
}
