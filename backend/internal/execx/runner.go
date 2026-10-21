package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
)

// DefaultMaxStreamBytes is the per-stream output cap from contract 3.1.
const DefaultMaxStreamBytes = 512 * 1024

// LineFunc receives decoded output lines. text keeps its trailing newline when
// the source produced one.
type LineFunc func(stream, text string)

// Request describes one child process invocation.
type Request struct {
	Tool           Tool
	Args           []string
	Timeout        time.Duration
	Env            []string
	Dir            string
	MaxStreamBytes int
	// Stdin is written to the child's standard input and closed afterwards. It
	// exists for secrets such as registry passwords (contract section 6.7);
	// callers must never put such values into Args or Display.
	Stdin []byte
	// Display overrides the human readable command line.
	Display string
}

// Result is the outcome of a synchronous invocation.
type Result struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	DurationMs int64
	Truncated  bool
	TimedOut   bool
	Canceled   bool
	Command    string
	ToolPath   string
}

// StreamResult is the outcome of a streaming invocation.
type StreamResult struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	Truncated  bool
	TimedOut   bool
	Canceled   bool
	DurationMs int64
	Command    string
	ToolPath   string
}

// Executor is the process execution surface used by the wsl/wslc services so
// that tests can substitute a fake.
type Executor interface {
	Run(ctx context.Context, request Request) (Result, error)
	Stream(ctx context.Context, request Request, onLine LineFunc) (StreamResult, error)
	Resolve(tool Tool) (string, error)
}

// Runner executes wsl.exe / wslc.exe with the encoding, timeout and process
// tree rules from contract section 6.
type Runner struct {
	Resolver *Resolver
}

// NewRunner returns a runner with the real resolver.
func NewRunner() *Runner {
	return &Runner{Resolver: NewResolver()}
}

var _ Executor = (*Runner)(nil)

// Resolve resolves a tool path.
func (r *Runner) Resolve(tool Tool) (string, error) {
	return r.Resolver.Resolve(tool)
}

// Run executes a command synchronously and captures its output.
func (r *Runner) Run(ctx context.Context, request Request) (Result, error) {
	streamed, err := r.Stream(ctx, request, nil)
	if err != nil {
		return Result{}, err
	}
	return Result{
		ExitCode:   streamed.ExitCode,
		Stdout:     streamed.Stdout,
		Stderr:     streamed.Stderr,
		DurationMs: streamed.DurationMs,
		Truncated:  streamed.Truncated,
		TimedOut:   streamed.TimedOut,
		Canceled:   streamed.Canceled,
		Command:    streamed.Command,
		ToolPath:   streamed.ToolPath,
	}, nil
}

// Stream executes a command, forwarding decoded output lines as they arrive.
func (r *Runner) Stream(ctx context.Context, request Request, onLine LineFunc) (StreamResult, error) {
	path, err := r.Resolver.Resolve(request.Tool)
	if err != nil {
		return StreamResult{}, err
	}
	runContext := ctx
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		runContext, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	command := exec.CommandContext(runContext, path, request.Args...)
	environment := os.Environ()
	if request.Tool == ToolWSL {
		// WSL_UTF8=1 keeps wsl.exe from encoding its output as UTF-16LE.
		environment = append(environment, "WSL_UTF8=1")
	}
	environment = append(environment, request.Env...)
	command.Env = environment
	command.Dir = request.Dir
	prepareCommand(command)
	if request.Stdin != nil {
		command.Stdin = bytes.NewReader(request.Stdin)
	}

	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return StreamResult{}, fmt.Errorf("创建 stdout 管道失败: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return StreamResult{}, fmt.Errorf("创建 stderr 管道失败: %w", err)
	}
	startedAt := time.Now()
	if err := command.Start(); err != nil {
		if ctx.Err() != nil {
			return StreamResult{Canceled: true, ExitCode: -1, Command: displayCommand(request.Tool, request.Args)}, nil
		}
		return StreamResult{}, fmt.Errorf("启动 %s 失败: %w", request.Tool, err)
	}
	stopKill := context.AfterFunc(runContext, func() { killProcessTree(command.Process.Pid) })
	defer stopKill()

	limit := request.MaxStreamBytes
	if limit <= 0 {
		limit = DefaultMaxStreamBytes
	}
	stdoutBuffer := newLimitedBuffer(limit)
	stderrBuffer := newLimitedBuffer(limit)
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		drain(stdoutPipe, "stdout", stdoutBuffer, onLine)
	}()
	go func() {
		defer readers.Done()
		drain(stderrPipe, "stderr", stderrBuffer, onLine)
	}()
	readers.Wait()
	waitErr := command.Wait()

	result := StreamResult{
		ExitCode:   exitCodeOf(waitErr),
		Stdout:     stdoutBuffer.String(),
		Stderr:     stderrBuffer.String(),
		Truncated:  stdoutBuffer.truncated || stderrBuffer.truncated,
		TimedOut:   errors.Is(runContext.Err(), context.DeadlineExceeded),
		Canceled:   ctx.Err() != nil,
		DurationMs: time.Since(startedAt).Milliseconds(),
		Command:    displayCommand(request.Tool, request.Args),
		ToolPath:   path,
	}
	if request.Display != "" {
		result.Command = request.Display
	}
	return result, nil
}

func drain(reader io.Reader, stream string, buffer *limitedBuffer, onLine LineFunc) {
	assembler := newLineAssembler()
	chunk := make([]byte, 32*1024)
	for {
		read, err := reader.Read(chunk)
		if read > 0 {
			emitLines(assembler.Write(chunk[:read]), stream, buffer, onLine)
		}
		if err != nil {
			break
		}
	}
	emitLines(assembler.Flush(), stream, buffer, onLine)
}

func emitLines(lines []string, stream string, buffer *limitedBuffer, onLine LineFunc) {
	for _, line := range lines {
		if onLine != nil {
			onLine(stream, line)
		}
		buffer.WriteString(line)
	}
}

// exitCodeOf normalizes the Windows exit status (0xFFFFFFFF surfaces as
// 4294967295) to -1.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return normalizeExitCode(exitError.ExitCode())
	}
	return -1
}

func normalizeExitCode(code int) int {
	if code > 2147483647 {
		return code - 4294967296
	}
	return code
}

// limitedBuffer accumulates decoded text up to a byte limit and reports
// truncation without blocking the reader.
type limitedBuffer struct {
	mutex     sync.Mutex
	buffer    strings.Builder
	limit     int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) WriteString(value string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.limit <= 0 || b.buffer.Len() >= b.limit {
		b.truncated = b.truncated || value != ""
		return
	}
	remaining := b.limit - b.buffer.Len()
	if len(value) > remaining {
		b.buffer.WriteString(value[:remaining])
		b.truncated = true
		return
	}
	b.buffer.WriteString(value)
}

func (b *limitedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.String()
}

// displayCommand renders an argv as a readable command line for logs. It is
// never executed.
func displayCommand(tool Tool, args []string) string {
	elements := make([]string, 0, len(args)+1)
	elements = append(elements, string(tool))
	for _, argument := range args {
		elements = append(elements, quoteDisplayArgument(argument))
	}
	return strings.Join(elements, " ")
}

func quoteDisplayArgument(argument string) string {
	if argument == "" {
		return "''"
	}
	if !strings.ContainsAny(argument, " \t\"'&|<>^()$`") {
		return argument
	}
	escaped := strings.ReplaceAll(argument, "'", `'\''`)
	return "'" + escaped + "'"
}

// ToAppError converts a launcher error into the structured error model.
func ToAppError(err error) *apperr.Error {
	var notFound *ToolNotFoundError
	if errors.As(err, &notFound) {
		return apperr.ToolNotFound(string(notFound.Tool)+".exe", notFound.Candidates)
	}
	return apperr.Internal("子进程启动失败", err.Error())
}

// ParseIntArg is a small helper for optional numeric arguments.
func ParseIntArg(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
