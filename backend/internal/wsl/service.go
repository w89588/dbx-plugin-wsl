// Package wsl implements the `system/*`, `wsl/*` and `wslc/info` style RPC
// methods that drive wsl.exe (distributions, configuration, installation).
package wsl

import (
	"context"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
)

// Service implements the distribution side of the contract.
type Service struct {
	Ex   execx.Executor
	Jobs *jobs.Manager
}

// New returns a wsl service.
func New(executor execx.Executor, jobManager *jobs.Manager) *Service {
	return &Service{Ex: executor, Jobs: jobManager}
}

// runTool executes one wsl/wslc command synchronously.
func (s *Service) runTool(ctx context.Context, tool execx.Tool, timeout time.Duration, args ...string) (execx.Result, *apperr.Error) {
	result, err := s.Ex.Run(ctx, execx.Request{Tool: tool, Args: args, Timeout: timeout})
	if err != nil {
		return result, execx.ToAppError(err)
	}
	if result.TimedOut {
		return result, apperr.Timeout("命令执行超时", result.Stderr)
	}
	return result, nil
}

// run executes a wsl.exe command.
func (s *Service) run(ctx context.Context, timeout time.Duration, args ...string) (execx.Result, *apperr.Error) {
	return s.runTool(ctx, execx.ToolWSL, timeout, args...)
}

// startJob registers a long running task and returns {jobId}.
func (s *Service) startJob(method string, emitter *dbxpluginsdk.Emitter, task jobs.Task) (any, *apperr.Error) {
	job := s.Jobs.Start(method, emitter, task)
	return map[string]any{"jobId": job.ID()}, nil
}

// streamJob runs a child process for a job, forwarding output lines. It marks
// the job failed for non-zero exits, except when the job was cancelled.
func (s *Service) streamJob(
	ctx context.Context,
	job *jobs.Job,
	request execx.Request,
	onLine execx.LineFunc,
) (execx.StreamResult, *apperr.Error) {
	result, err := s.Ex.Stream(ctx, request, onLine)
	if err != nil {
		job.SetExitCode(-1)
		return result, execx.ToAppError(err)
	}
	job.SetExitCode(result.ExitCode)
	if ctx.Err() != nil {
		return result, nil
	}
	if result.TimedOut {
		return result, apperr.Timeout("命令执行超时", result.Stderr)
	}
	if result.ExitCode != 0 {
		return result, commandFailure("wsl", request.Args, result.ExitCode, result.Stdout, result.Stderr)
	}
	return result, nil
}

// launchFailure converts a failed wsl.exe invocation into a structured error.
func (s *Service) launchFailure(operation string, result execx.Result) *apperr.Error {
	if result.ExitCode == 0 {
		return nil
	}
	return commandFailure("wsl", nil, result.ExitCode, result.Stdout, result.Stderr)
}

// commandFailure classifies a non-zero child process exit.
func commandFailure(tool string, args []string, exitCode int, stdout, stderr string) *apperr.Error {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	switch {
	case apperr.LooksNotFound(detail):
		return apperr.NotFound("目标不存在", "请刷新列表后重试", detail)
	case apperr.LooksConflict(detail):
		hint := "请先停止相关对象再重试"
		if apperr.LooksSharingViolation(detail) {
			hint = "VHD 正在使用中：请先执行 wsl --shutdown，再重试该操作"
		}
		return apperr.Conflict("操作与当前状态冲突", hint, detail)
	case apperr.LooksPermission(detail):
		return apperr.ProcessFailed("需要管理员权限", "请以管理员身份运行 DBX 或使用提升后的终端", detail)
	case apperr.LooksUnsupported(detail):
		return apperr.Unsupported("当前 WSL 版本不支持该操作", "请升级 WSL 后重试", detail)
	default:
		return apperr.ProcessFailed(
			tool+" 命令执行失败",
			"请检查命令参数与系统状态",
			detail,
		)
	}
}

// isWSLError reports whether a wsl.exe invocation failed at the WSL layer
// rather than inside the executed command.
func isWSLError(result execx.Result) bool {
	if result.ExitCode < 0 {
		return true
	}
	if result.ExitCode == 0 {
		return false
	}
	combined := result.Stderr + "\n" + result.Stdout
	return apperr.ContainsAny(combined,
		"wsl_e_", "wsl/", "错误代码", "error code", "无法启动", "failed to start", "e_invalidarg",
	)
}

// validateName rejects empty or control-character distribution names.
func validateName(value string) (string, *apperr.Error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", apperr.InvalidParams("name 不能为空", "请提供发行版名称", "")
	}
	if len(name) > 256 || strings.ContainsAny(name, "\r\n\t\x00") {
		return "", apperr.InvalidParams("name 不合法", "发行版名称长度需在 1-256 之间且不能包含控制字符", "")
	}
	return name, nil
}
