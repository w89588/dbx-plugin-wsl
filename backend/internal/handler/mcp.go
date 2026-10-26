package handler

import (
	"bytes"
	"context"
	"encoding/json"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"io"
	"reflect"
	"strings"
)

type mcpMethod struct {
	Method   string
	ReadOnly bool
	Params   any
}

// Schemas follow service parameter types; semantic validation stays in services.
func mcpSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				props[name] = mcpSchema(f.Type)
			}
		}
		return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": mcpSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": mcpSchema(t.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	default:
		return map[string]any{"type": "string"}
	}
}

func mcpTools() any {
	tool := func(name, description string, readOnly bool, props map[string]any, required []string) any {
		return map[string]any{"name": name, "description": description,
			"annotations": map[string]any{"readOnlyHint": readOnly, "destructiveHint": !readOnly, "idempotentHint": false, "openWorldHint": true},
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}}
	}
	method := map[string]any{"type": "string", "description": "Exact RPC method from wsl_describe. Only allowlisted methods are accepted."}
	params := map[string]any{"type": "string", "description": "JSON-encoded parameters object; default {}. Use wsl_describe for fields/types. WSL exec command is a string; WSLC exec command is a string array."}
	return map[string]any{"tools": []any{
		tool("wsl_connections", "List this Windows user's WSL distribution connections with name, state, default, WSL version and disk paths. Use names with wsl/distros/*. WSLC containers are listed separately with wsl_read method=wslc/containers/list.", true, map[string]any{}, []string{}),
		tool("wsl_describe", "Discover all WSL Manager read/write RPC methods or get one method's parameter schema. Omit method to list. Call before wsl_read/wsl_write. Long operations return jobId; jobs/get retrieves final status/output, jobs/cancel cancels. Data belongs to the Windows user running DBX.", true, map[string]any{"method": method}, []string{}),
		tool("wsl_read", "Query WSL distributions/connections, WSLC containers, images, volumes, networks, sessions, status, configuration and jobs. Use wsl_describe for exact methods and paramsJson. Logs require follow=false. Exec, terminal launches and event subscriptions require wsl_write.", true, map[string]any{"method": method, "paramsJson": params}, []string{"method"}),
		tool("wsl_write", "Full WSL/WSLC management: install/import/export/start/stop/delete distributions, execute commands and read/write files, run/exec/remove containers, manage images/volumes/networks/registry credentials, configuration/idle policy and jobs. Get exact parameters from wsl_describe. Destructive operations can permanently delete data. Long operations return jobId, NOT completion; poll jobs/get. Registry passwords go only to stdin, but host/client may retain tool arguments. Logs follow=true uses this tool. Diagnostics (diagnostics/run) also require this tool because they can probe user-specified registry hosts and local ports, even when no targets are supplied. Requires host write authorization.", false, map[string]any{"method": method, "paramsJson": params}, []string{"method"}),
	}}
}

func strictJSON(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (h *Handler) mcpCall(raw json.RawMessage, emitter *dbxpluginsdk.Emitter) (any, *dbxpluginsdk.PluginError) {
	var request struct {
		Tool         string          `json:"tool"`
		Arguments    json.RawMessage `json:"arguments"`
		Lifecycle    json.RawMessage `json:"lifecycle"`
		ConnectionID string          `json:"connectionId"`
	}
	// Do not echo request payloads or decoder diagnostics: they can carry secrets.
	invalid := func(message string) (any, *dbxpluginsdk.PluginError) {
		return mcpResult(nil, apperr.InvalidParams(message, "使用 wsl_describe 查询方法与参数", "")), nil
	}
	if err := strictJSON(raw, &request); err != nil {
		return invalid("MCP 请求参数不合法")
	}
	args := request.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	if len(bytes.TrimSpace(args)) == 0 || bytes.TrimSpace(args)[0] != '{' {
		return invalid("arguments 必须为对象")
	}
	var arguments struct {
		Method     string `json:"method"`
		ParamsJSON string `json:"paramsJson"`
	}
	switch request.Tool {
	case "wsl_connections":
		var empty struct{}
		if err := strictJSON(args, &empty); err != nil {
			return invalid("wsl_connections 不接受参数")
		}
		result, appErr := h.dispatch(context.Background(), "wsl/distros/list", json.RawMessage(`{}`), emitter)
		return mcpResult(result, appErr), nil
	case "wsl_describe":
		var describe struct {
			Method string `json:"method"`
		}
		if err := strictJSON(args, &describe); err != nil {
			return invalid("wsl_describe 参数不合法")
		}
		methods := []any{}
		for _, m := range mcpMethods {
			if describe.Method != "" && describe.Method != m.Method {
				continue
			}
			methods = append(methods, describeMethod(m))
		}
		if len(methods) == 0 {
			return invalid("未知方法")
		}
		return mcpResult(map[string]any{"methods": methods, "notes": []string{
			"字段类型由服务参数生成；模板、备份和配置等关键流程附带约束与副作用说明。未列出的语义仍由服务验证，不代表字段可任意省略。",
			"发行版名称、容器 ID 与路径属于运行 DBX 的当前 Windows 用户，不是远程连接。",
			"长任务返回 jobId；通过 wsl_read jobs/get 读取 status、error、result 和输出，completed 才是成功。任务仅在当前 sidecar 生命周期内有效。",
			"wslc/containers/logs 的 follow=true 必须使用 wsl_write；日志跟随和事件订阅结束后调用 jobs/cancel。",
			"system/config/write target 为 wslconfig 或 wslc，须先读取并传入 expectedRevision，备份且检测版本冲突；保活设置用 system/wsl/idle/write 并传 expectedRevision。",
			"exec 和 console/run 可以执行任意命令与文件读写，始终归入写工具；先取得用户对具体操作的授权。",
		}}, nil), nil
	case "wsl_read", "wsl_write":
		if err := strictJSON(args, &arguments); err != nil {
			return invalid("工具参数不合法")
		}
	default:
		return mcpResult(nil, apperr.NotFound("未知 MCP 工具", "调用 mcp/tools 查询工具列表", "")), nil
	}
	var selected *mcpMethod
	for i := range mcpMethods {
		if mcpMethods[i].Method == arguments.Method {
			selected = &mcpMethods[i]
			break
		}
	}
	if selected == nil {
		return invalid("未知或不允许通过 MCP 调用的方法")
	}
	params := []byte(arguments.ParamsJSON)
	if len(params) == 0 {
		params = []byte(`{}`)
	}
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return invalid("paramsJson 必须是 JSON 对象字符串")
	}
	target := reflect.New(reflect.TypeOf(selected.Params)).Interface()
	if err := strictJSON(params, target); err != nil {
		return invalid("方法参数不合法：请检查字段名与类型")
	}
	readOnly := selected.ReadOnly
	// Following logs creates a background task; it cannot bypass write approval.
	if arguments.Method == "wslc/containers/logs" {
		var logs struct {
			Follow bool `json:"follow"`
		}
		_ = json.Unmarshal(params, &logs)
		readOnly = !logs.Follow
	}
	if request.Tool == "wsl_read" && !readOnly {
		return invalid("此方法执行写操作或创建任务，请使用 wsl_write")
	}
	result, appErr := h.dispatch(context.Background(), selected.Method, params, emitter)
	if appErr == nil {
		if output, ok := result.(map[string]any); ok {
			if exitCode, ok := output["exitCode"].(int); ok && exitCode != 0 {
				return mcpResult(result, apperr.ProcessFailed("命令执行失败", "检查退出码与输出", "")), nil
			}
		}
	}
	return mcpResult(result, appErr), nil
}

func mcpResult(value any, appErr *apperr.Error) any {
	payload := value
	if appErr != nil {
		payload = map[string]any{"error": map[string]any{"code": appErr.Code, "message": appErr.Message, "hint": appErr.Hint, "detail": appErr.Detail}, "result": value}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(`{"error":{"code":"INTERNAL","message":"结果编码失败"}}`)
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}, "isError": appErr != nil || err != nil, "resultType": "complete"}
}
