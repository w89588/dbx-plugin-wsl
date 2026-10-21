// Package wslc implements the `wslc/*` RPC methods (WSL containers, images,
// volumes, networks and sessions) by driving wslc.exe.
package wslc

import (
	"context"
	"sort"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
)

// Service implements the wslc side of the contract.
type Service struct {
	Ex   execx.Executor
	Jobs *jobs.Manager
}

// New returns a wslc service.
func New(executor execx.Executor, jobManager *jobs.Manager) *Service {
	return &Service{Ex: executor, Jobs: jobManager}
}

// run executes one wslc command synchronously.
func (s *Service) run(ctx context.Context, timeout time.Duration, args ...string) (execx.Result, *apperr.Error) {
	result, err := s.Ex.Run(ctx, execx.Request{Tool: execx.ToolWSLc, Args: args, Timeout: timeout})
	if err != nil {
		return result, execx.ToAppError(err)
	}
	if result.TimedOut {
		return result, apperr.Timeout("wslc 命令执行超时", result.Stderr)
	}
	return result, nil
}

// startJob registers a long running task and returns {jobId}.
func (s *Service) startJob(method string, emitter *dbxpluginsdk.Emitter, task jobs.Task) (any, *apperr.Error) {
	job := s.Jobs.Start(method, emitter, task)
	return map[string]any{"jobId": job.ID()}, nil
}

// streamJob runs a child process for a job, forwarding output lines.
func (s *Service) streamJob(
	ctx context.Context,
	job *jobs.Job,
	args []string,
	onLine execx.LineFunc,
) (execx.StreamResult, *apperr.Error) {
	return s.streamJobWith(ctx, job, args, onLine, nil)
}

// streamJobWith is streamJob with an optional failure classifier. Pull/build
// pass the registry classifier so job/end.error carries auth/rate-limit codes
// while job/output keeps the raw wslc text (contract 4.3).
func (s *Service) streamJobWith(
	ctx context.Context,
	job *jobs.Job,
	args []string,
	onLine execx.LineFunc,
	classify func(operation, stdout, stderr string) *apperr.Error,
) (execx.StreamResult, *apperr.Error) {
	result, err := s.Ex.Stream(ctx, execx.Request{Tool: execx.ToolWSLc, Args: args}, onLine)
	if err != nil {
		job.SetExitCode(-1)
		return result, execx.ToAppError(err)
	}
	job.SetExitCode(result.ExitCode)
	if ctx.Err() != nil {
		return result, nil
	}
	if result.TimedOut {
		return result, apperr.Timeout("wslc 命令执行超时", result.Stderr)
	}
	if result.ExitCode != 0 {
		if classify != nil {
			return result, classify("wslc 任务失败", result.Stdout, result.Stderr)
		}
		return result, failure("wslc 任务失败", result.Stdout, result.Stderr)
	}
	return result, nil
}

// failure classifies a non-zero wslc exit into the structured error model.
func failure(operation, stdout, stderr string) *apperr.Error {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	switch {
	case apperr.LooksNotFound(detail):
		return apperr.NotFound("目标不存在", "请刷新列表后重试", detail)
	case apperr.LooksConflict(detail):
		return apperr.Conflict("操作与当前状态冲突", "请先停止相关对象或使用强制选项", detail)
	case apperr.LooksPermission(detail):
		return apperr.ProcessFailed("权限不足", "请以管理员身份运行 DBX 后重试", detail)
	case apperr.LooksUnsupported(detail):
		return apperr.Unsupported("当前 wslc 版本不支持该操作", "请升级 WSL 后重试", detail)
	default:
		return apperr.ProcessFailed(operation, "请检查命令参数与 wslc 状态", detail)
	}
}

// outputText returns the trimmed stdout, falling back to stderr.
func outputText(result execx.Result) string {
	text := strings.TrimSpace(result.Stdout)
	if text == "" {
		text = strings.TrimSpace(result.Stderr)
	}
	return text
}

// sortedEnv renders an env map as KEY=VALUE pairs in deterministic order.
func sortedEnv(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+values[key])
	}
	return pairs
}

func requireID(value string) (string, *apperr.Error) {
	id := strings.TrimSpace(value)
	if id == "" {
		return "", apperr.InvalidParams("id 不能为空", "请提供容器 ID 或名称", "")
	}
	if len(id) > 256 || strings.ContainsAny(id, "\r\n\t\x00") {
		return "", apperr.InvalidParams("id 不合法", "请检查容器 ID", "")
	}
	return id, nil
}

func requireText(field, value string) (string, *apperr.Error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", apperr.InvalidParams(field+" 不能为空", "请检查请求参数", "")
	}
	if strings.ContainsAny(trimmed, "\r\n\t\x00") {
		return "", apperr.InvalidParams(field+" 不合法", "不能包含控制字符", "")
	}
	return trimmed, nil
}

func requireList(field string, values []string) ([]string, *apperr.Error) {
	if len(values) == 0 {
		return nil, apperr.InvalidParams(field+" 不能为空", "请至少提供一个条目", "")
	}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || strings.ContainsAny(trimmed, "\r\n\t\x00") {
			return nil, apperr.InvalidParams(field+" 包含非法条目", "请检查请求参数", value)
		}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned, nil
}
