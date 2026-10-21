// Package jobs implements the long running task manager described by
// docs/rpc-contract.md section 3.2 and 4.5.
package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
)

const (
	// MaxLines is the number of output lines kept for jobs/get compensation.
	MaxLines = 2000
	// MaxBytes is the byte budget of the retained output.
	MaxBytes = 512 * 1024
)

// Status is a job lifecycle state.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Line is one buffered output line.
type Line struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
	TS     int64  `json:"ts"`
}

// Task is the body of a job. It must return the job result and a structured
// failure (nil on success). Cancellation is detected through ctx.
type Task func(ctx context.Context, job *Job) (any, *apperr.Error)

// Manager owns all jobs of the sidecar process.
type Manager struct {
	mutex sync.Mutex
	seq   int
	jobs  map[string]*Job
	now   func() time.Time
}

// NewManager returns an empty job manager.
func NewManager() *Manager {
	return &Manager{jobs: map[string]*Job{}, now: time.Now}
}

// Job is a long running task. Its public surface is safe for concurrent use.
type Job struct {
	manager   *Manager
	id        string
	method    string
	emitter   *dbxpluginsdk.Emitter
	startedAt time.Time
	cancel    context.CancelFunc
	done      chan struct{}

	mutex      sync.Mutex
	lines      []Line
	lineBytes  int
	status     Status
	exitCode   int
	failure    *apperr.Error
	result     any
	finishedAt time.Time
}

// Start registers and launches a job, returning immediately.
func (m *Manager) Start(method string, emitter *dbxpluginsdk.Emitter, task Task) *Job {
	m.mutex.Lock()
	m.seq++
	job := &Job{
		manager:   m,
		id:        fmt.Sprintf("job-%d", m.seq),
		method:    method,
		emitter:   emitter,
		startedAt: m.now(),
		status:    StatusRunning,
		done:      make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel
	m.jobs[job.id] = job
	m.mutex.Unlock()

	go job.run(ctx, task)
	return job
}

func (j *Job) run(ctx context.Context, task Task) {
	// The JSON-RPC response carrying {jobId} is written right after the handler
	// returns. Yield briefly so the very first job event can never overtake it
	// on the wire: UI awaits invoke() to learn the jobId, then follows events.
	select {
	case <-time.After(10 * time.Millisecond):
	case <-ctx.Done():
	}
	var result any
	var failure *apperr.Error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				failure = apperr.Internal("任务内部错误", fmt.Sprint(recovered))
			}
		}()
		result, failure = task(ctx, j)
	}()
	j.finish(ctx, result, failure)
}

func (j *Job) finish(ctx context.Context, result any, failure *apperr.Error) {
	j.mutex.Lock()
	j.result = result
	j.failure = failure
	j.finishedAt = j.manager.now()
	if ctx.Err() == context.Canceled {
		j.status = StatusCancelled
	} else if failure != nil {
		j.status = StatusFailed
	} else if j.exitCode != 0 {
		j.status = StatusFailed
		if j.failure == nil {
			j.failure = apperr.ProcessFailed("任务失败", "", "")
		}
		failure = j.failure
	} else {
		j.status = StatusCompleted
	}
	exitCode := j.exitCode
	j.mutex.Unlock()

	errorPayload := any(nil)
	if failure != nil {
		errorPayload = map[string]any{
			"code":   failure.Code,
			"hint":   failure.Hint,
			"detail": failure.Detail,
		}
	}
	j.emit("job/end", map[string]any{
		"jobId":      j.id,
		"ok":         exitCode == 0 && failure == nil,
		"exitCode":   exitCode,
		"error":      errorPayload,
		"result":     result,
		"durationMs": j.durationMs(),
	})
	close(j.done)
}

// ID returns the job identifier (job-N).
func (j *Job) ID() string { return j.id }

// Method returns the RPC method that started the job.
func (j *Job) Method() string { return j.method }

// Done is closed when job/end has been emitted.
func (j *Job) Done() <-chan struct{} { return j.done }

// SetExitCode records the child process exit code.
func (j *Job) SetExitCode(code int) {
	j.mutex.Lock()
	j.exitCode = code
	j.mutex.Unlock()
}

// ExitCode returns the recorded child process exit code.
func (j *Job) ExitCode() int {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	return j.exitCode
}

// Line appends an output line to the compensation buffer and emits
// job/output. text keeps its trailing newline when present.
func (j *Job) Line(stream, text string) {
	timestamp := j.manager.now().UnixMilli()
	j.mutex.Lock()
	j.lines = append(j.lines, Line{Stream: stream, Text: text, TS: timestamp})
	j.lineBytes += len(text)
	for len(j.lines) > MaxLines || j.lineBytes > MaxBytes {
		if len(j.lines) == 0 {
			break
		}
		j.lineBytes -= len(j.lines[0].Text)
		j.lines = j.lines[1:]
	}
	j.mutex.Unlock()
	j.emit("job/output", map[string]any{
		"jobId":  j.id,
		"stream": stream,
		"text":   text,
		"ts":     timestamp,
	})
}

// Event emits a custom event belonging to the job (used by wslc events).
func (j *Job) Event(method string, params any) {
	j.emit(method, params)
}

func (j *Job) emit(method string, params any) {
	if j.emitter == nil {
		return
	}
	_ = j.emitter.Event(method, params)
}

func (j *Job) durationMs() int64 {
	j.mutex.Lock()
	started := j.startedAt
	finished := j.finishedAt
	j.mutex.Unlock()
	end := j.manager.now()
	if !finished.IsZero() {
		end = finished
	}
	return end.Sub(started).Milliseconds()
}

// Snapshot returns the jobs/get payload.
func (j *Job) Snapshot() map[string]any {
	j.mutex.Lock()
	lines := make([]Line, len(j.lines))
	copy(lines, j.lines)
	status := j.status
	exitCode := j.exitCode
	failure := j.failure
	result := j.result
	j.mutex.Unlock()

	errorPayload := any(nil)
	if failure != nil {
		errorPayload = map[string]any{
			"code":   failure.Code,
			"hint":   failure.Hint,
			"detail": failure.Detail,
		}
	}
	return map[string]any{
		"jobId":      j.id,
		"method":     j.method,
		"status":     string(status),
		"startedAt":  j.startedAt.UnixMilli(),
		"durationMs": j.durationMs(),
		"exitCode":   exitCode,
		"error":      errorPayload,
		"result":     result,
		"lines":      lines,
	}
}

// Get returns a job snapshot by id.
func (m *Manager) Get(id string) (map[string]any, bool) {
	m.mutex.Lock()
	job, ok := m.jobs[id]
	m.mutex.Unlock()
	if !ok {
		return nil, false
	}
	return job.Snapshot(), true
}

// List returns the currently running jobs.
func (m *Manager) List() []map[string]any {
	m.mutex.Lock()
	snapshot := make([]*Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		snapshot = append(snapshot, job)
	}
	m.mutex.Unlock()
	rows := make([]map[string]any, 0, len(snapshot))
	for _, job := range snapshot {
		job.mutex.Lock()
		running := job.status == StatusRunning
		startedAt := job.startedAt
		job.mutex.Unlock()
		if !running {
			continue
		}
		rows = append(rows, map[string]any{
			"jobId":     job.id,
			"method":    job.method,
			"startedAt": startedAt.UnixMilli(),
		})
	}
	return rows
}

// Cancel requests cancellation. It reports whether the job exists and whether
// a cancellation was actually delivered.
func (m *Manager) Cancel(id string) (bool, bool) {
	m.mutex.Lock()
	job, ok := m.jobs[id]
	m.mutex.Unlock()
	if !ok {
		return false, false
	}
	job.mutex.Lock()
	running := job.status == StatusRunning
	job.mutex.Unlock()
	if !running {
		return true, false
	}
	job.cancel()
	return true, true
}

// Now returns the manager clock (used by services for timestamps).
func (m *Manager) Now() time.Time { return m.now() }
