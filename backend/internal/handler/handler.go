// Package handler routes JSON-RPC methods to the wsl/wslc services and applies
// the parameter validation and error model of docs/rpc-contract.md.
package handler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
	"github.com/w89588/dbx-plugin-wsl/internal/wsl"
	"github.com/w89588/dbx-plugin-wsl/internal/wslc"
)

const methodNotFound = "METHOD_NOT_FOUND"

// Handler implements dbxpluginsdk.Handler for the WSL Manager plugin.
type Handler struct {
	WSL  *wsl.Service
	WSLC *wslc.Service
	Jobs *jobs.Manager
	Ex   execx.Executor
}

// New builds a handler backed by the given process executor.
func New(executor execx.Executor) *Handler {
	manager := jobs.NewManager()
	return &Handler{
		WSL:  wsl.New(executor, manager),
		WSLC: wslc.New(executor, manager),
		Jobs: manager,
		Ex:   executor,
	}
}

// Handle implements dbxpluginsdk.Handler.
func (h *Handler) Handle(
	_ dbxpluginsdk.RequestContext,
	method string,
	params json.RawMessage,
	emitter *dbxpluginsdk.Emitter,
) (any, *dbxpluginsdk.PluginError) {
	ctx := context.Background()
	result, appErr := h.dispatch(ctx, method, params, emitter)
	if appErr == nil {
		return result, nil
	}
	if appErr.Code == methodNotFound {
		return nil, dbxpluginsdk.MethodNotFound(method)
	}
	return nil, toPluginError(appErr)
}

func (h *Handler) dispatch(
	ctx context.Context,
	method string,
	params json.RawMessage,
	emitter *dbxpluginsdk.Emitter,
) (any, *apperr.Error) {
	switch method {
	// ---- system ----
	case "system/info":
		return h.WSL.SystemInfo(ctx)
	case "system/project/open":
		var request struct {
			Target string `json:"target"`
		}
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.OpenProjectLink(request.Target)
	case "system/wsl/status":
		return h.WSL.WSLStatus(ctx)
	case "system/config/read":
		return h.WSL.ConfigRead(ctx)
	case "system/wsl/idle/read":
		return h.WSL.IdleRead()
	case "system/wsl/idle/write":
		var request wsl.IdleWriteParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.IdleWrite(request)
	case "system/config/write":
		var request wsl.ConfigWriteParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.ConfigWrite(ctx, request)
	case "system/wsl/online":
		return h.WSL.Online(ctx)

	// ---- wsl distributions ----
	case "wsl/distros/list":
		return h.WSL.DistroList(ctx)
	case "wsl/distros/start":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroStart(ctx, name)
	case "wsl/distros/terminate":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroTerminate(ctx, name)
	case "wsl/distros/setDefault":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroSetDefault(ctx, name)
	case "wsl/distros/exec":
		var request wsl.ExecParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroExec(ctx, request)
	case "wsl/distros/info":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroInfo(ctx, name)
	case "wsl/distros/wslconf":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroWslConf(ctx, name)
	case "wsl/distros/export":
		var request wsl.ExportParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroExport(ctx, emitter, request)
	case "wsl/distros/import":
		var request wsl.ImportParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroImport(ctx, emitter, request)
	case "wsl/distros/unregister":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroUnregister(ctx, name)
	case "wsl/distros/setVersion":
		var request wsl.SetVersionParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroSetVersion(ctx, emitter, request)
	case "wsl/distros/sparse":
		var request wsl.SparseParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroSparse(ctx, request)
	case "wsl/distros/compact":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroCompact(ctx, name)
	case "wsl/distros/defaultUser":
		var request wsl.DefaultUserParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroDefaultUser(ctx, request)
	case "wsl/distros/resize":
		var request wsl.ResizeParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroResize(ctx, emitter, request)
	case "wsl/distros/manage":
		var request wsl.ManageParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroManage(ctx, emitter, request)
	case "wsl/distros/openTerminal":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroOpenTerminal(ctx, name)
	case "wsl/distros/openExplorer":
		name, appErr := decodeName(params)
		if appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroOpenExplorer(ctx, name)
	case "wsl/shutdown":
		var request wsl.ShutdownParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.Shutdown(ctx, request)
	case "wsl/update":
		var request wsl.UpdateParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.Update(ctx, emitter, request)
	case "wsl/distros/install":
		var request wsl.InstallParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSL.DistroInstall(ctx, emitter, request)

	// ---- wslc ----
	case "wslc/info":
		return h.WSLC.Info(ctx)
	case "wslc/containers/list":
		var request wslc.ListParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersList(ctx, request)
	case "wslc/containers/inspect":
		var request wslc.InspectParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersInspect(ctx, request.ID)
	case "wslc/containers/start":
		var request wslc.StartParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersStart(ctx, request)
	case "wslc/containers/stop":
		var request wslc.StopParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersStop(ctx, request)
	case "wslc/containers/restart":
		var request wslc.RestartParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersRestart(ctx, request)
	case "wslc/containers/kill":
		var request wslc.KillParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersKill(ctx, request)
	case "wslc/containers/remove":
		var request wslc.RemoveParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersRemove(ctx, request)
	case "wslc/containers/run":
		var request wslc.RunParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersRun(ctx, request)
	case "wslc/containers/exec":
		var request wslc.ExecParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersExec(ctx, request)
	case "wslc/containers/logs":
		var request wslc.LogsParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersLogs(ctx, emitter, request)
	case "wslc/containers/stats":
		var request wslc.StatsParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ContainersStats(ctx, request)
	case "wslc/containers/prune":
		return h.WSLC.ContainersPrune(ctx)
	case "wslc/images/list":
		return h.WSLC.ImagesList(ctx)
	case "wslc/images/inspect":
		var request wslc.ImageInspectParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ImagesInspect(ctx, request.Ref)
	case "wslc/images/pull":
		var request wslc.PullParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ImagesPull(ctx, emitter, request)
	case "wslc/images/remove":
		var request wslc.RemoveImagesParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ImagesRemove(ctx, request)
	case "wslc/images/prune":
		var request wslc.PruneImagesParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ImagesPrune(ctx, emitter, request)
	case "wslc/images/tag":
		var request wslc.TagParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ImagesTag(ctx, request)
	case "wslc/images/build":
		var request wslc.BuildParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ImagesBuild(ctx, emitter, request)
	case "wslc/images/save":
		var request wslc.SaveParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ImagesSave(ctx, emitter, request)
	case "wslc/images/load":
		var request wslc.LoadParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.ImagesLoad(ctx, emitter, request)
	case "wslc/volumes/list":
		return h.WSLC.VolumesList(ctx)
	case "wslc/volumes/inspect":
		var request wslc.VolumeInspectParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.VolumesInspect(ctx, request.Name)
	case "wslc/volumes/create":
		var request wslc.CreateVolumeParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.VolumesCreate(ctx, request)
	case "wslc/volumes/remove":
		var request wslc.RemoveVolumesParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.VolumesRemove(ctx, request)
	case "wslc/volumes/prune":
		return h.WSLC.VolumesPrune(ctx)
	case "wslc/networks/list":
		return h.WSLC.NetworksList(ctx)
	case "wslc/networks/inspect":
		var request wslc.NetworkInspectParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.NetworksInspect(ctx, request.Name)
	case "wslc/networks/create":
		var request wslc.CreateNetworkParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.NetworksCreate(ctx, request)
	case "wslc/networks/remove":
		var request wslc.RemoveNetworksParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.NetworksRemove(ctx, request)
	case "wslc/networks/connect":
		var request wslc.NetworkLinkParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.NetworksConnect(ctx, request)
	case "wslc/networks/disconnect":
		var request wslc.NetworkLinkParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.NetworksDisconnect(ctx, request)
	case "wslc/networks/prune":
		return h.WSLC.NetworksPrune(ctx)
	case "wslc/sessions/list":
		return h.WSLC.SessionsList(ctx)
	case "wslc/events/start":
		var request wslc.EventsParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.EventsStart(ctx, emitter, request)
	case "wslc/events/stop":
		return h.cancelJob(params, "stopped")
	case "wslc/registry/login":
		var request wslc.LoginParams
		// Never echo raw params here: they contain the registry password.
		if appErr := decodeSensitiveParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.RegistryLogin(ctx, request)
	case "wslc/registry/logout":
		var request wslc.LogoutParams
		if appErr := decodeParams(params, &request); appErr != nil {
			return nil, appErr
		}
		return h.WSLC.RegistryLogout(ctx, request)

	// ---- console ----
	case "console/run":
		return h.consoleRun(ctx, params)
	case "console/presets":
		return map[string]any{"presets": consolePresets()}, nil

	// ---- jobs ----
	case "jobs/list":
		return map[string]any{"jobs": h.Jobs.List()}, nil
	case "jobs/get":
		return h.jobGet(params)
	case "jobs/cancel":
		return h.cancelJob(params, "cancelled")

	default:
		return nil, &apperr.Error{Code: methodNotFound, Message: "Method not found: " + method}
	}
}

func decodeParams(params json.RawMessage, target any) *apperr.Error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	if err := json.Unmarshal(params, target); err != nil {
		return apperr.InvalidParams("请求参数不合法", "请检查参数类型", err.Error()+"\n"+string(params))
	}
	return nil
}

// decodeSensitiveParams is decodeParams for requests that carry a secret
// (wslc/registry/login). The raw params are never echoed into the error detail
// because they include the password; json unmarshal messages only describe
// types, never values (contract 6.7).
func decodeSensitiveParams(params json.RawMessage, target any) *apperr.Error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	if err := json.Unmarshal(params, target); err != nil {
		return apperr.InvalidParams("请求参数不合法", "请检查参数类型", err.Error())
	}
	return nil
}

func decodeName(params json.RawMessage) (string, *apperr.Error) {
	var request struct {
		Name string `json:"name"`
	}
	if appErr := decodeParams(params, &request); appErr != nil {
		return "", appErr
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return "", apperr.InvalidParams("name 不能为空", "请提供发行版名称", "")
	}
	return name, nil
}

func decodeJobID(params json.RawMessage) (string, *apperr.Error) {
	var request struct {
		JobID string `json:"jobId"`
	}
	if appErr := decodeParams(params, &request); appErr != nil {
		return "", appErr
	}
	jobID := strings.TrimSpace(request.JobID)
	if jobID == "" {
		return "", apperr.InvalidParams("jobId 不能为空", "请提供任务 ID", "")
	}
	return jobID, nil
}

func (h *Handler) jobGet(params json.RawMessage) (any, *apperr.Error) {
	jobID, appErr := decodeJobID(params)
	if appErr != nil {
		return nil, appErr
	}
	snapshot, ok := h.Jobs.Get(jobID)
	if !ok {
		return nil, apperr.NotFound("任务不存在: "+jobID, "请刷新任务列表", "")
	}
	return snapshot, nil
}

func (h *Handler) cancelJob(params json.RawMessage, field string) (any, *apperr.Error) {
	jobID, appErr := decodeJobID(params)
	if appErr != nil {
		return nil, appErr
	}
	found, cancelled := h.Jobs.Cancel(jobID)
	if !found {
		return nil, apperr.NotFound("任务不存在: "+jobID, "请刷新任务列表", "")
	}
	if field == "stopped" {
		return map[string]any{"stopped": cancelled}, nil
	}
	return map[string]any{"jobId": jobID, "cancelled": cancelled}, nil
}

// ConsoleRunParams is the request of console/run.
type ConsoleRunParams struct {
	Tool      string   `json:"tool"`
	Args      []string `json:"args"`
	TimeoutMs int      `json:"timeoutMs"`
}

func (h *Handler) consoleRun(ctx context.Context, params json.RawMessage) (any, *apperr.Error) {
	var request ConsoleRunParams
	if appErr := decodeParams(params, &request); appErr != nil {
		return nil, appErr
	}
	tool := strings.ToLower(strings.TrimSpace(request.Tool))
	var execTool execx.Tool
	switch tool {
	case "wsl":
		execTool = execx.ToolWSL
	case "wslc":
		execTool = execx.ToolWSLc
	default:
		return nil, apperr.InvalidParams("tool 必须是 wsl 或 wslc", "请检查请求参数", request.Tool)
	}
	for _, argument := range request.Args {
		if strings.ContainsAny(argument, "\r\n\x00") {
			return nil, apperr.InvalidParams("args 包含非法参数", "参数不能包含控制字符", argument)
		}
	}
	timeout := 20 * time.Second
	if request.TimeoutMs > 0 {
		timeout = time.Duration(request.TimeoutMs) * time.Millisecond
		if timeout > 120*time.Second {
			timeout = 120 * time.Second
		}
	}
	result, err := h.Ex.Run(ctx, execx.Request{Tool: execTool, Args: request.Args, Timeout: timeout})
	if err != nil {
		return nil, execx.ToAppError(err)
	}
	if result.TimedOut {
		return nil, apperr.Timeout("命令执行超时", result.Stderr)
	}
	return map[string]any{
		"tool":       tool,
		"exitCode":   result.ExitCode,
		"stdout":     result.Stdout,
		"stderr":     result.Stderr,
		"durationMs": result.DurationMs,
		"truncated":  result.Truncated,
		"command":    result.Command,
	}, nil
}

func consolePresets() []map[string]any {
	return []map[string]any{
		{"id": "wsl-list", "label": "wsl -l -v", "tool": "wsl", "args": []string{"-l", "-v"}},
		{"id": "wsl-status", "label": "wsl --status", "tool": "wsl", "args": []string{"--status"}},
		{"id": "wsl-version", "label": "wsl --version", "tool": "wsl", "args": []string{"--version"}},
		{"id": "wsl-online", "label": "wsl --list --online", "tool": "wsl", "args": []string{"--list", "--online"}},
		{"id": "wslc-info", "label": "wslc info --format json", "tool": "wslc", "args": []string{"info", "--format", "json"}},
		{"id": "wslc-list", "label": "wslc list --all", "tool": "wslc", "args": []string{"list", "--all"}},
		{"id": "wslc-images", "label": "wslc images", "tool": "wslc", "args": []string{"images"}},
		{"id": "wslc-stats", "label": "wslc stats", "tool": "wslc", "args": []string{"stats"}},
		{"id": "wslc-networks", "label": "wslc network list", "tool": "wslc", "args": []string{"network", "list"}},
		{"id": "wslc-volumes", "label": "wslc volume list", "tool": "wslc", "args": []string{"volume", "list"}},
		{"id": "wslc-version", "label": "wslc version", "tool": "wslc", "args": []string{"version"}},
	}
}

// toPluginError converts a structured service error into the JSON-RPC error
// envelope with data={code,hint,detail}.
func toPluginError(appErr *apperr.Error) *dbxpluginsdk.PluginError {
	return &dbxpluginsdk.PluginError{
		Code:    -32000,
		Message: appErr.Message,
		Data: map[string]any{
			"code":   appErr.Code,
			"hint":   appErr.Hint,
			"detail": appErr.Detail,
		},
	}
}
