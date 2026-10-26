package handler

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/w89588/dbx-plugin-wsl/internal/execx"
)

func mcpInvoke(t *testing.T, h *Handler, tool, method string, params any) (map[string]any, map[string]any) {
	t.Helper()
	encoded, _ := json.Marshal(params)
	args := map[string]any{"method": method, "paramsJson": string(encoded)}
	if tool == "wsl_describe" {
		args = map[string]any{"method": method}
	}
	if tool == "wsl_connections" {
		args = map[string]any{}
	}
	raw, _ := json.Marshal(map[string]any{"tool": tool, "arguments": args})
	result, rpcErr := call(t, h, "mcp/call", string(raw))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	envelope := result.(map[string]any)
	var payload map[string]any
	if err := json.Unmarshal([]byte(envelope["content"].([]any)[0].(map[string]any)["text"].(string)), &payload); err != nil {
		t.Fatal(err)
	}
	return envelope, payload
}

func TestMCPCatalogMatchesDispatch(t *testing.T) {
	// Guard full coverage as ordinary RPC methods are added or renamed.
	file, err := parser.ParseFile(token.NewFileSet(), "handler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatch" {
			return true
		}
		switchStmt := fn.Body.List[0].(*ast.SwitchStmt)
		for _, node := range switchStmt.Body.List {
			for _, expr := range node.(*ast.CaseClause).List {
				name, _ := strconv.Unquote(expr.(*ast.BasicLit).Value)
				found[name] = true
			}
		}
		return false
	})
	seen := map[string]bool{}
	for _, m := range mcpMethods {
		if !found[m.Method] || seen[m.Method] {
			t.Fatalf("invalid/duplicate method %s", m.Method)
		}
		seen[m.Method] = true
	}
	for method := range found {
		if !seen[method] {
			t.Errorf("MCP missing method %s", method)
		}
	}
}

func TestMCPDiscoveryDoesNotExecuteAndSchemaTypes(t *testing.T) {
	h := New(&fakeExecutor{run: func(context.Context, execx.Request) (execx.Result, error) {
		t.Fatal("discovery executed a command")
		return execx.Result{}, nil
	}})
	result, rpcErr := call(t, h, "mcp/tools", "{}")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	tools := result.(map[string]any)["tools"].([]any)
	if len(tools) != 4 {
		t.Fatal(len(tools))
	}
	for _, entry := range tools {
		tool := entry.(map[string]any)
		if tool["annotations"].(map[string]any)["readOnlyHint"] != (tool["name"] != "wsl_write") {
			t.Fatal(tool)
		}
	}
	for _, tc := range []struct{ method, kind string }{{"wsl/distros/exec", "string"}, {"wslc/containers/exec", "array"}} {
		env, payload := mcpInvoke(t, h, "wsl_describe", tc.method, nil)
		if env["isError"] != false {
			t.Fatal(payload)
		}
		field := payload["methods"].([]any)[0].(map[string]any)["inputSchema"].(map[string]any)["properties"].(map[string]any)["command"].(map[string]any)
		if field["type"] != tc.kind {
			t.Fatal(field)
		}
	}
}

func TestMCPReadCannotExecuteWrites(t *testing.T) {
	h := New(&fakeExecutor{run: func(context.Context, execx.Request) (execx.Result, error) {
		t.Fatal("read executed write")
		return execx.Result{}, nil
	}})
	for _, method := range []string{"wsl/distros/exec", "wslc/containers/exec", "wsl/distros/unregister", "wsl/shutdown", "wslc/containers/remove", "wslc/images/prune", "wslc/volumes/prune", "wslc/networks/prune", "system/config/write", "system/wsl/idle/write", "wslc/registry/login", "console/run", "wslc/events/start", "jobs/cancel"} {
		env, payload := mcpInvoke(t, h, "wsl_read", method, map[string]any{})
		if env["isError"] != true || payload["error"].(map[string]any)["code"] != "INVALID_PARAMS" {
			t.Fatalf("%s: %#v", method, payload)
		}
	}
	env, _ := mcpInvoke(t, h, "wsl_read", "wslc/containers/logs", map[string]any{"id": "x", "follow": true})
	if env["isError"] != true {
		t.Fatal(env)
	}
	if len(h.Jobs.List()) != 0 {
		t.Fatal("read created jobs")
	}
}

func TestMCPReadsWritesAndExitStatus(t *testing.T) {
	calls := []execx.Request{}
	h := New(&fakeExecutor{run: func(_ context.Context, r execx.Request) (execx.Result, error) {
		calls = append(calls, r)
		if r.Tool == execx.ToolWSLc && r.Args[0] == "list" {
			return execx.Result{Stdout: `{"ID":"abc","Image":"alpine","Names":"test","State":"running"}`}, nil
		}
		return execx.Result{ExitCode: 0, Stdout: "ok"}, nil
	}})
	env, payload := mcpInvoke(t, h, "wsl_read", "wslc/containers/list", map[string]any{"all": true})
	if env["isError"] != false || payload["containers"] == nil {
		t.Fatal(env, payload)
	}
	env, _ = mcpInvoke(t, h, "wsl_write", "wslc/containers/start", map[string]any{"id": "abc"})
	if env["isError"] != false || !strings.Contains(strings.Join(calls[len(calls)-1].Args, " "), "start") {
		t.Fatal(env, calls)
	}
	h.Ex = &fakeExecutor{run: func(context.Context, execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: 7, Stdout: "failed"}, nil
	}}
	env, payload = mcpInvoke(t, h, "wsl_write", "console/run", map[string]any{"tool": "wsl", "args": []string{"--status"}})
	if env["isError"] != true {
		t.Fatal(env, payload)
	}
}

func TestMCPStrictParamsAndSecretRedaction(t *testing.T) {
	h := New(&fakeExecutor{})
	for _, params := range []string{`{"tool":"wsl_write","arguments":{"method":"wslc/registry/login","paramsJson":"{\"server\":[],\"password\":\"private-secret\"}"}}`, `{"tool":"wsl_read","arguments":{"method":"mcp/call"}}`, `{"tool":"unknown"}`, `{"tool":"wsl_read","arguments":[]}`, `{"tool":"wsl_read","arguments":{"method":"jobs/list","paramsJson":"[]"}}`, `{"tool":"wsl_read","arguments":{"method":"jobs/list","paramsJson":"{\"typo\":true}"}}`, `{"tool":"wsl_read","arguments":{"method":"jobs/list","paramsJson":"{} {}"}}`} {
		result, rpcErr := call(t, h, "mcp/call", params)
		if rpcErr != nil {
			t.Fatal(rpcErr)
		}
		raw, _ := json.Marshal(result)
		if result.(map[string]any)["isError"] != true || strings.Contains(string(raw), "private-secret") {
			t.Fatal(string(raw))
		}
	}
	secret := "private-secret"
	h = New(&fakeExecutor{run: func(_ context.Context, r execx.Request) (execx.Result, error) {
		if strings.Contains(strings.Join(r.Args, " "), secret) || !strings.Contains(string(r.Stdin), secret) {
			t.Fatal("credential transport", r.Args)
		}
		return execx.Result{ExitCode: 1, Stderr: secret + " unauthorized"}, nil
	}})
	env, payload := mcpInvoke(t, h, "wsl_write", "wslc/registry/login", map[string]any{"server": "docker.io", "username": "test", "password": secret})
	raw, _ := json.Marshal(payload)
	if env["isError"] != true || strings.Contains(string(raw), secret) {
		t.Fatal(env, payload)
	}
}

func TestMCPJobsCompletionAndCancellation(t *testing.T) {
	h := New(&fakeExecutor{stream: func(_ context.Context, _ execx.Request, line execx.LineFunc) (execx.StreamResult, error) {
		line("stdout", "pulled\n")
		return execx.StreamResult{}, nil
	}})
	env, payload := mcpInvoke(t, h, "wsl_write", "wslc/images/pull", map[string]any{"ref": "alpine:3.20"})
	if env["isError"] != false {
		t.Fatal(payload)
	}
	jobID := payload["jobId"].(string)
	job, ok := h.Jobs.Get(jobID)
	if !ok {
		t.Fatal(jobID)
	}
	_ = job
	deadline := time.Now().Add(time.Second)
	for {
		_, payload = mcpInvoke(t, h, "wsl_read", "jobs/get", map[string]any{"jobId": jobID})
		if payload["status"] != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(payload)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if payload["status"] != "completed" {
		t.Fatal(payload)
	}
	h = New(&fakeExecutor{stream: func(ctx context.Context, _ execx.Request, _ execx.LineFunc) (execx.StreamResult, error) {
		<-ctx.Done()
		return execx.StreamResult{}, nil
	}})
	_, payload = mcpInvoke(t, h, "wsl_write", "wslc/events/start", map[string]any{})
	jobID = payload["jobId"].(string)
	env, payload = mcpInvoke(t, h, "wsl_write", "jobs/cancel", map[string]any{"jobId": jobID})
	if env["isError"] != false || payload["cancelled"] != true {
		t.Fatal(payload)
	}
	deadline = time.Now().Add(time.Second)
	for {
		_, payload = mcpInvoke(t, h, "wsl_read", "jobs/get", map[string]any{"jobId": jobID})
		if payload["status"] != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(payload)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if payload["status"] != "cancelled" {
		t.Fatal(payload)
	}
}

func TestMCPConfigurationWriteIsolated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	h := New(&fakeExecutor{})
	_, policy := mcpInvoke(t, h, "wsl_read", "system/wsl/idle/read", map[string]any{})
	env, payload := mcpInvoke(t, h, "wsl_write", "system/wsl/idle/write", map[string]any{"mode": "keepAlive", "expectedRevision": policy["revision"]})
	if env["isError"] != false {
		t.Fatal(payload)
	}
	contents, err := os.ReadFile(filepath.Join(home, ".wslconfig"))
	if err != nil || !strings.Contains(string(contents), "instanceIdleTimeout=-1") {
		t.Fatal(string(contents), err)
	}
	env, payload = mcpInvoke(t, h, "wsl_write", "system/wsl/idle/write", map[string]any{"mode": "default", "expectedRevision": policy["revision"]})
	if env["isError"] != true || payload["error"].(map[string]any)["code"] != "CONFLICT" {
		t.Fatal(payload)
	}
}
