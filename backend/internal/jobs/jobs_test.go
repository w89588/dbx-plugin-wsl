package jobs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
)

func waitFor(t *testing.T, job *Job) {
	t.Helper()
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("job did not finish in time")
	}
}

func TestJobCompletesAndBuffersLines(t *testing.T) {
	manager := NewManager()
	job := manager.Start("wslc/images/pull", nil, func(ctx context.Context, job *Job) (any, *apperr.Error) {
		job.SetExitCode(0)
		job.Line("stdout", "Pulling from library/alpine\n")
		job.Line("stderr", "warning: x\n")
		return map[string]any{"ref": "alpine:3.20"}, nil
	})
	waitFor(t, job)
	snapshot, ok := manager.Get(job.ID())
	if !ok {
		t.Fatal("job not found after completion")
	}
	if snapshot["status"] != "completed" {
		t.Fatalf("unexpected status: %#v", snapshot["status"])
	}
	if snapshot["exitCode"] != 0 {
		t.Fatalf("unexpected exit code: %#v", snapshot["exitCode"])
	}
	lines, ok := snapshot["lines"].([]Line)
	if !ok || len(lines) != 2 {
		t.Fatalf("unexpected lines: %#v", snapshot["lines"])
	}
	if lines[0].Stream != "stdout" || lines[0].Text != "Pulling from library/alpine\n" || lines[0].TS == 0 {
		t.Fatalf("unexpected line: %#v", lines[0])
	}
	result, ok := snapshot["result"].(map[string]any)
	if !ok || result["ref"] != "alpine:3.20" {
		t.Fatalf("unexpected result: %#v", snapshot["result"])
	}
	if snapshot["error"] != nil {
		t.Fatalf("unexpected error: %#v", snapshot["error"])
	}
}

func TestJobFailureAndCancellation(t *testing.T) {
	manager := NewManager()
	failed := manager.Start("wslc/images/pull", nil, func(ctx context.Context, job *Job) (any, *apperr.Error) {
		job.SetExitCode(1)
		return nil, &apperr.Error{Code: apperr.CodeProcessFailed, Message: "pull failed", Detail: "denied"}
	})
	waitFor(t, failed)
	snapshot, _ := manager.Get(failed.ID())
	if snapshot["status"] != "failed" {
		t.Fatalf("unexpected status: %#v", snapshot["status"])
	}
	errorPayload, ok := snapshot["error"].(map[string]any)
	if !ok || errorPayload["code"] != apperr.CodeProcessFailed || errorPayload["detail"] != "denied" {
		t.Fatalf("unexpected error payload: %#v", snapshot["error"])
	}

	blocked := manager.Start("wslc/containers/logs", nil, func(ctx context.Context, job *Job) (any, *apperr.Error) {
		<-ctx.Done()
		return nil, nil
	})
	found, cancelled := manager.Cancel(blocked.ID())
	if !found || !cancelled {
		t.Fatalf("unexpected cancel result: found=%v cancelled=%v", found, cancelled)
	}
	waitFor(t, blocked)
	snapshot, _ = manager.Get(blocked.ID())
	if snapshot["status"] != "cancelled" {
		t.Fatalf("unexpected cancelled status: %#v", snapshot["status"])
	}
	if found, cancelled := manager.Cancel("job-999"); found || cancelled {
		t.Fatalf("unknown job should not cancel: %v %v", found, cancelled)
	}
}

func TestJobListOnlyRunningAndLineCap(t *testing.T) {
	manager := NewManager()
	blocked := manager.Start("wslc/events/start", nil, func(ctx context.Context, job *Job) (any, *apperr.Error) {
		for index := 0; index < MaxLines+50; index++ {
			job.Line("stdout", "line\n")
		}
		job.Line("stdout", strings.Repeat("x", MaxBytes+1024))
		<-ctx.Done()
		return nil, nil
	})
	deadline := time.Now().Add(5 * time.Second)
	for len(manager.List()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	list := manager.List()
	if len(list) != 1 || list[0]["jobId"] != blocked.ID() {
		t.Fatalf("unexpected list: %#v", list)
	}
	found, cancelled := manager.Cancel(blocked.ID())
	if !found || !cancelled {
		t.Fatalf("cancel failed: %v %v", found, cancelled)
	}
	waitFor(t, blocked)
	if len(manager.List()) != 0 {
		t.Fatalf("finished job must not be listed: %#v", manager.List())
	}
	snapshot, _ := manager.Get(blocked.ID())
	lines := snapshot["lines"].([]Line)
	if len(lines) > MaxLines {
		t.Fatalf("line cap violated: %d lines", len(lines))
	}
	bytes := 0
	for _, line := range lines {
		bytes += len(line.Text)
	}
	if bytes > MaxBytes {
		t.Fatalf("byte cap violated: %d bytes", bytes)
	}
}

func TestHistoryBoundAndSequences(t *testing.T) {
	manager := NewManager()
	fixedNow := time.Now()
	manager.now = func() time.Time { return fixedNow }
	running := manager.Start("running", nil, func(ctx context.Context, _ *Job) (any, *apperr.Error) { <-ctx.Done(); return nil, nil })
	defer manager.Cancel(running.ID())
	var first *Job
	for i := 0; i < MaxCompleted+3; i++ {
		job := manager.Start("quick", nil, func(_ context.Context, j *Job) (any, *apperr.Error) {
			j.Line("stdout", "one\n")
			j.Line("stdout", "two\n")
			return nil, nil
		})
		if first == nil {
			first = job
		}
		waitFor(t, job)
		snapshot, ok := manager.Get(job.ID())
		if !ok {
			t.Fatal("new job evicted")
		}
		lines := snapshot["lines"].([]Line)
		if snapshot["sequence"] != uint64(3) || lines[0].Sequence != 1 || lines[1].Sequence != 2 {
			t.Fatal(snapshot)
		}
	}
	if len(manager.List(true)) != MaxCompleted+1 {
		t.Fatalf("history cap: %d", len(manager.List(true)))
	}
	if _, ok := manager.Get(first.ID()); ok {
		t.Fatal("oldest completed job not evicted")
	}
	if len(manager.List()) != 1 {
		t.Fatal("running job lost")
	}
	manager.mutex.Lock()
	for _, job := range manager.jobs {
		job.mutex.Lock()
		if job.status != StatusRunning {
			job.finishedAt = time.Now().Add(-Retention - time.Hour)
		}
		job.mutex.Unlock()
	}
	manager.mutex.Unlock()
	if len(manager.List(true)) != 1 {
		t.Fatal("expired history retained")
	}
}
