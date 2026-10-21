package execx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestResolver() *Resolver {
	return &Resolver{
		cache:    map[Tool]resolvedTool{},
		ttl:      ResolveTTL,
		now:      time.Now,
		lookPath: exec.LookPath,
		stat:     os.Stat,
	}
}

func TestResolverFindsSystemRootWSLFirst(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SystemRoot", directory)
	system32 := filepath.Join(directory, "System32")
	if err := os.MkdirAll(system32, 0o755); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(system32, "wsl.exe")
	if err := os.WriteFile(expected, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := newTestResolver()
	path, err := resolver.Resolve(ToolWSL)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.EqualFold(path, expected) {
		t.Fatalf("got %q want %q", path, expected)
	}
}

func TestResolverCachesAndExpires(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SystemRoot", directory)
	t.Setenv("PATH", filepath.Join(directory, "empty-path"))
	t.Setenv("LOCALAPPDATA", filepath.Join(directory, "LocalAppData"))
	system32 := filepath.Join(directory, "System32")
	if err := os.MkdirAll(system32, 0o755); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(system32, "wsl.exe")
	if err := os.WriteFile(expected, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := newTestResolver()
	base := time.Now()
	resolver.now = func() time.Time { return base }
	if _, err := resolver.Resolve(ToolWSL); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if err := os.Remove(expected); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ToolWSL); err != nil {
		t.Fatalf("cached resolve should succeed: %v", err)
	}
	resolver.now = func() time.Time { return base.Add(ResolveTTL + time.Minute) }
	if _, err := resolver.Resolve(ToolWSL); err == nil {
		t.Fatal("expected cache expiry to re-probe and fail")
	} else {
		var notFound *ToolNotFoundError
		if !errorsAs(err, &notFound) {
			t.Fatalf("expected ToolNotFoundError, got %T", err)
		}
		if len(notFound.Candidates) == 0 {
			t.Fatal("expected candidate paths in error")
		}
	}
}

func errorsAs(err error, target **ToolNotFoundError) bool {
	value, ok := err.(*ToolNotFoundError)
	if ok {
		*target = value
	}
	return ok
}

func TestResolverCandidatesOrder(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SystemRoot", directory)
	t.Setenv("ProgramFiles", filepath.Join(directory, "ProgramFiles"))
	t.Setenv("LOCALAPPDATA", filepath.Join(directory, "LocalAppData"))
	resolver := newTestResolver()
	wslCandidates := resolver.Candidates(ToolWSL)
	if len(wslCandidates) == 0 {
		t.Fatal("expected wsl candidates")
	}
	if !strings.HasSuffix(strings.ToLower(wslCandidates[0]), `system32\wsl.exe`) {
		t.Fatalf("unexpected first candidate: %q", wslCandidates[0])
	}
	wslcCandidates := resolver.Candidates(ToolWSLc)
	found := false
	for _, candidate := range wslcCandidates {
		if strings.EqualFold(candidate, `C:\Program Files\WSL\wslc.exe`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Program Files candidate fallback, got %#v", wslcCandidates)
	}
}
