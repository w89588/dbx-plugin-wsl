package wslc

import (
	"context"
	"strconv"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
)

// ListParams is the request of wslc/containers/list.
type ListParams struct {
	All *bool `json:"all"`
}

// ContainersList implements wslc/containers/list.
func (s *Service) ContainersList(ctx context.Context, params ListParams) (any, *apperr.Error) {
	all := true
	if params.All != nil {
		all = *params.All
	}
	args := []string{"list", "--format", "json", "--no-trunc"}
	if all {
		args = append(args, "--all")
	}
	result, appErr := s.run(ctx, 60*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("列出容器失败", result.Stdout, result.Stderr)
	}
	containers, warnings := parse.Containers(result.Stdout)
	return map[string]any{
		"containers": containers,
		"count":      len(containers),
		"warnings":   warnings,
	}, nil
}

// InspectParams is the request of wslc/containers/inspect.
type InspectParams struct {
	ID string `json:"id"`
}

// ContainersInspect implements wslc/containers/inspect.
func (s *Service) ContainersInspect(ctx context.Context, id string) (any, *apperr.Error) {
	identifier, appErr := requireID(id)
	if appErr != nil {
		return nil, appErr
	}
	result, appErr := s.run(ctx, 60*time.Second, "inspect", "--type", "container", "--format", "json", identifier)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, inspectFailure("检查容器失败", result)
	}
	value, err := parse.FirstJSONValue([]byte(result.Stdout))
	if err != nil {
		return nil, apperr.Internal("解析容器详情失败", result.Stdout)
	}
	return map[string]any{"raw": value}, nil
}

func inspectFailure(operation string, result execx.Result) *apperr.Error {
	normalized := failure(operation, result.Stdout, result.Stderr)
	if normalized.Code == apperr.CodeNotFound {
		return apperr.NotFound("目标不存在", "请刷新列表后重试", normalized.Detail)
	}
	if strings.TrimSpace(result.Stdout) == "[]" || strings.TrimSpace(result.Stdout) == "" {
		return apperr.NotFound("目标不存在", "请刷新列表后重试", normalized.Detail)
	}
	return normalized
}

// StartParams is the request of wslc/containers/start.
type StartParams struct {
	ID      string `json:"id"`
	Timeout int    `json:"timeout"`
}

// ContainersStart implements wslc/containers/start.
func (s *Service) ContainersStart(ctx context.Context, params StartParams) (any, *apperr.Error) {
	return s.containerAction(ctx, "start", params.ID, []string{"start"}, params.Timeout, 120*time.Second)
}

// StopParams is the request of wslc/containers/stop.
type StopParams struct {
	ID      string `json:"id"`
	Timeout int    `json:"timeout"`
}

// ContainersStop implements wslc/containers/stop.
func (s *Service) ContainersStop(ctx context.Context, params StopParams) (any, *apperr.Error) {
	extra := []string{"stop"}
	if params.Timeout > 0 {
		extra = append(extra, "-t", strconv.Itoa(params.Timeout))
	}
	return s.containerAction(ctx, "stop", params.ID, extra, 0, 120*time.Second)
}

// RestartParams is the request of wslc/containers/restart.
type RestartParams struct {
	ID      string `json:"id"`
	Timeout int    `json:"timeout"`
}

// ContainersRestart implements wslc/containers/restart.
func (s *Service) ContainersRestart(ctx context.Context, params RestartParams) (any, *apperr.Error) {
	extra := []string{"restart"}
	if params.Timeout > 0 {
		extra = append(extra, "-t", strconv.Itoa(params.Timeout))
	}
	return s.containerAction(ctx, "restart", params.ID, extra, 0, 180*time.Second)
}

// KillParams is the request of wslc/containers/kill.
type KillParams struct {
	ID     string `json:"id"`
	Signal string `json:"signal"`
}

// ContainersKill implements wslc/containers/kill.
func (s *Service) ContainersKill(ctx context.Context, params KillParams) (any, *apperr.Error) {
	extra := []string{"kill"}
	if signal := strings.TrimSpace(params.Signal); signal != "" {
		extra = append(extra, "--signal", signal)
	}
	return s.containerAction(ctx, "kill", params.ID, extra, 0, 60*time.Second)
}

// RemoveParams is the request of wslc/containers/remove.
type RemoveParams struct {
	ID    string `json:"id"`
	Force bool   `json:"force"`
}

// ContainersRemove implements wslc/containers/remove.
func (s *Service) ContainersRemove(ctx context.Context, params RemoveParams) (any, *apperr.Error) {
	id, appErr := requireID(params.ID)
	if appErr != nil {
		return nil, appErr
	}
	args := []string{"remove"}
	if params.Force {
		args = append(args, "--force")
	}
	args = append(args, id)
	result, appErr := s.run(ctx, 120*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("移除容器失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"id": id, "action": "remove", "output": outputText(result), "exitCode": 0}, nil
}

func (s *Service) containerAction(
	ctx context.Context,
	action, id string,
	prefix []string,
	timeoutSeconds int,
	timeout time.Duration,
) (any, *apperr.Error) {
	identifier, appErr := requireID(id)
	if appErr != nil {
		return nil, appErr
	}
	args := append([]string(nil), prefix...)
	if timeoutSeconds > 0 && (action == "stop" || action == "restart") {
		args = append(args, "-t", strconv.Itoa(timeoutSeconds))
	}
	args = append(args, identifier)
	result, appErr := s.run(ctx, timeout, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("容器操作失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"id": identifier, "action": action, "output": outputText(result), "exitCode": 0}, nil
}

// RunParams is the request of wslc/containers/run.
type RunParams struct {
	Image   string            `json:"image"`
	Name    string            `json:"name"`
	Detach  *bool             `json:"detach"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env"`
	Ports   []string          `json:"ports"`
	Volumes []string          `json:"volumes"`
	Workdir string            `json:"workdir"`
	User    string            `json:"user"`
	CPUs    string            `json:"cpus"`
	Memory  string            `json:"memory"`
	Rm      bool              `json:"rm"`
}

// ContainersRun implements wslc/containers/run. Detached mode is always used
// (contract 4.3); the request's detach flag is accepted but ignored.
func (s *Service) ContainersRun(ctx context.Context, params RunParams) (any, *apperr.Error) {
	image, appErr := requireText("image", params.Image)
	if appErr != nil {
		return nil, appErr
	}
	args := []string{"run", "--detach"}
	name := strings.TrimSpace(params.Name)
	if name != "" {
		name, appErr = requireText("name", name)
		if appErr != nil {
			return nil, appErr
		}
		args = append(args, "--name", name)
	}
	for _, pair := range sortedEnv(params.Env) {
		args = append(args, "--env", pair)
	}
	for _, port := range params.Ports {
		trimmed := strings.TrimSpace(port)
		if trimmed == "" {
			continue
		}
		args = append(args, "--publish", trimmed)
	}
	for _, volume := range params.Volumes {
		trimmed := strings.TrimSpace(volume)
		if trimmed == "" {
			continue
		}
		args = append(args, "--volume", trimmed)
	}
	if workdir := strings.TrimSpace(params.Workdir); workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	if user := strings.TrimSpace(params.User); user != "" {
		args = append(args, "--user", user)
	}
	if cpus := strings.TrimSpace(params.CPUs); cpus != "" {
		args = append(args, "--cpus", cpus)
	}
	if memory := strings.TrimSpace(params.Memory); memory != "" {
		args = append(args, "--memory", memory)
	}
	if params.Rm {
		args = append(args, "--rm")
	}
	args = append(args, image)
	for _, argument := range params.Command {
		args = append(args, argument)
	}
	result, appErr := s.run(ctx, 120*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		// `wslc run` pulls missing images implicitly; apply the same registry
		// failure classification as wslc/images/pull (contract 4.3).
		return nil, classifyRegistryFailure("启动容器失败", result.Stdout, result.Stderr)
	}
	identifier := firstLineField(result.Stdout)
	containerName := name
	if identifier == "" && name != "" {
		identifier = name
	}
	if identifier == "" {
		return nil, apperr.Internal("无法解析新容器 ID", result.Stdout+result.Stderr)
	}
	if resolvedID, resolvedName := s.resolveContainer(ctx, identifier, name); resolvedID != "" || resolvedName != "" {
		// Prefer the full ID from the list over the possibly abbreviated id
		// printed by `wslc run`.
		if resolvedID != "" && len(resolvedID) >= len(identifier) {
			identifier = resolvedID
		}
		if resolvedName != "" {
			containerName = resolvedName
		}
	}
	return map[string]any{"id": identifier, "name": containerName}, nil
}

// resolveContainer looks up a container by id or name using the list output.
func (s *Service) resolveContainer(ctx context.Context, identifier, name string) (string, string) {
	result, appErr := s.run(ctx, 30*time.Second, "list", "--all", "--no-trunc", "--format", "json")
	if appErr != nil || result.ExitCode != 0 {
		return "", ""
	}
	containers, _ := parse.Containers(result.Stdout)
	for _, container := range containers {
		if (name != "" && strings.EqualFold(container.Name, name)) || strings.HasPrefix(container.ID, identifier) || container.ID == identifier {
			return container.ID, container.Name
		}
	}
	return "", ""
}

func firstLineField(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// ExecParams is the request of wslc/containers/exec.
type ExecParams struct {
	ID      string            `json:"id"`
	Command []string          `json:"command"`
	User    string            `json:"user"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
}

// ContainersExec implements wslc/containers/exec.
func (s *Service) ContainersExec(ctx context.Context, params ExecParams) (any, *apperr.Error) {
	id, appErr := requireID(params.ID)
	if appErr != nil {
		return nil, appErr
	}
	if len(params.Command) == 0 {
		return nil, apperr.InvalidParams("command 不能为空", "请提供要在容器内执行的命令", "")
	}
	for _, argument := range params.Command {
		if strings.ContainsAny(argument, "\r\n\x00") {
			return nil, apperr.InvalidParams("command 参数不合法", "命令参数不能包含控制字符", "")
		}
	}
	args := []string{"exec"}
	if user := strings.TrimSpace(params.User); user != "" {
		args = append(args, "--user", user)
	}
	if workdir := strings.TrimSpace(params.Workdir); workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	for _, pair := range sortedEnv(params.Env) {
		args = append(args, "--env", pair)
	}
	args = append(args, id)
	args = append(args, params.Command...)
	result, appErr := s.run(ctx, 60*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode < 0 || containerCommandFailed(result) {
		return nil, failure("容器内命令执行失败", result.Stdout, result.Stderr)
	}
	return runResult(result), nil
}

// containerCommandFailed distinguishes a wslc layer failure from a non-zero
// exit code of the command executed inside the container. Only wslc's own
// error envelope (WSLC_E_*) or a missing container is treated as an RPC error;
// the command's own stderr must stay a RunResult.
func containerCommandFailed(result execx.Result) bool {
	if result.ExitCode < 0 {
		return true
	}
	if result.ExitCode == 0 {
		return false
	}
	combined := result.Stderr + "\n" + result.Stdout
	return apperr.ContainsAny(combined, "wslc_e_", "找不到容器", "no such container", "container is not running")
}

func runResult(result execx.Result) map[string]any {
	return map[string]any{
		"exitCode":   result.ExitCode,
		"stdout":     result.Stdout,
		"stderr":     result.Stderr,
		"durationMs": result.DurationMs,
		"truncated":  result.Truncated,
		"command":    result.Command,
	}
}

// LogsParams is the request of wslc/containers/logs.
type LogsParams struct {
	ID         string `json:"id"`
	Tail       *int   `json:"tail"`
	Timestamps bool   `json:"timestamps"`
	Follow     bool   `json:"follow"`
}

// ContainersLogs implements both the snapshot and the follow variant.
func (s *Service) ContainersLogs(ctx context.Context, emitter *dbxpluginsdk.Emitter, params LogsParams) (any, *apperr.Error) {
	id, appErr := requireID(params.ID)
	if appErr != nil {
		return nil, appErr
	}
	args := []string{"logs"}
	if params.Tail != nil {
		if *params.Tail < 0 {
			return nil, apperr.InvalidParams("tail 必须大于等于 0", "请检查请求参数", "")
		}
		// wslc rejects "--tail 0"; omitting the flag is the closest behavior
		// (it also avoids failing follow mode with an explicit tail of 0).
		if *params.Tail > 0 {
			args = append(args, "--tail", strconv.Itoa(*params.Tail))
		}
	}
	if params.Timestamps {
		args = append(args, "--timestamps")
	}
	if params.Follow {
		args = append(args, "--follow")
	}
	args = append(args, id)
	if params.Follow {
		return s.startJob("wslc/containers/logs", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
			_, appErr := s.streamJob(jobCtx, job, args, func(stream, text string) {
				job.Line(stream, text)
			})
			if appErr != nil {
				return nil, appErr
			}
			return map[string]any{"id": id}, nil
		})
	}
	result, appErr := s.run(ctx, 60*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("读取容器日志失败", result.Stdout, result.Stderr)
	}
	return runResult(result), nil
}

// StatsParams is the request of wslc/containers/stats.
type StatsParams struct {
	All *bool `json:"all"`
}

// ContainersStats implements wslc/containers/stats.
func (s *Service) ContainersStats(ctx context.Context, params StatsParams) (any, *apperr.Error) {
	args := []string{"stats", "--format", "json", "--no-trunc"}
	if params.All != nil && *params.All {
		args = append(args, "--all")
	}
	result, appErr := s.run(ctx, 60*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("读取容器统计失败", result.Stdout, result.Stderr)
	}
	stats, warnings := parse.Stats(result.Stdout)
	return map[string]any{"stats": stats, "warnings": warnings}, nil
}

// ContainersPrune implements wslc/containers/prune.
func (s *Service) ContainersPrune(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 120*time.Second, "container", "prune", "--force")
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("清理容器失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"output": outputText(result)}, nil
}
