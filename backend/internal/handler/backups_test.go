package handler

import (
	"context"
	"encoding/json"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func awaitJob(t *testing.T, h *Handler, value any) map[string]any {
	t.Helper()
	id := value.(map[string]any)["jobId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := h.Jobs.Get(id)
		if snapshot["status"] != "running" {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job timeout")
	return nil
}
func TestBackupIntegrityAndRestoreBoundaries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "profile"))
	imports := 0
	h := New(&fakeExecutor{run: func(_ context.Context, r execx.Request) (execx.Result, error) {
		if len(r.Args) > 1 && r.Args[0] == "--list" && r.Args[1] == "--verbose" {
			return execx.Result{Stdout: "  NAME STATE VERSION\n* TestSource    Stopped    2\n"}, nil
		}
		return execx.Result{}, nil
	}, stream: func(_ context.Context, r execx.Request, _ execx.LineFunc) (execx.StreamResult, error) {
		if r.Args[0] == "--export" {
			return execx.StreamResult{}, os.WriteFile(r.Args[2], []byte("archive"), 0600)
		}
		if r.Args[0] == "--import" {
			imports++
		}
		return execx.StreamResult{}, nil
	}})
	raw, _ := json.Marshal(map[string]any{"name": "TestSource", "directory": dir})
	value, e := call(t, h, "backups/create", string(raw))
	if e != nil {
		t.Fatal(e)
	}
	done := awaitJob(t, h, value)
	if done["status"] != "completed" {
		t.Fatal(done)
	}
	list, e := call(t, h, "backups/list", "{}")
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(list)
	var records struct{ Backups []struct{ ID, Path string } }
	json.Unmarshal(encoded, &records)
	if len(records.Backups) != 1 {
		t.Fatal(string(encoded))
	}
	r := records.Backups[0]
	restore := func(name, location string) (any, bool) {
		raw, _ := json.Marshal(map[string]any{"id": r.ID, "name": name, "location": location})
		v, e := call(t, h, "backups/restore", string(raw))
		return v, e != nil
	}
	if _, blocked := restore("TestSource", filepath.Join(dir, "copy")); !blocked {
		t.Fatal("source overwrite accepted")
	}
	if _, blocked := restore("Clone", dir); !blocked {
		t.Fatal("existing folder accepted")
	}
	if err := os.WriteFile(r.Path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	v, blocked := restore("Clone", filepath.Join(dir, "clone"))
	if blocked {
		t.Fatal("expected asynchronous verification")
	}
	done = awaitJob(t, h, v)
	if done["status"] != "failed" || imports != 0 {
		t.Fatalf("corrupt archive imported: %v %d", done, imports)
	}
}
