package handler

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/execx"
)

type fakeExecutor struct {
	resolveErr error
	run        func(context.Context, execx.Request) (execx.Result, error)
	stream     func(context.Context, execx.Request, execx.LineFunc) (execx.StreamResult, error)
}

func (f *fakeExecutor) Resolve(_ execx.Tool) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return `C:\Fake\wsl.exe`, nil
}

func (f *fakeExecutor) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if f.run != nil {
		return f.run(ctx, request)
	}
	return execx.Result{ExitCode: 0, Command: strings.Join(request.Args, " ")}, nil
}

func (f *fakeExecutor) Stream(ctx context.Context, request execx.Request, onLine execx.LineFunc) (execx.StreamResult, error) {
	if f.stream != nil {
		return f.stream(ctx, request, onLine)
	}
	return execx.StreamResult{ExitCode: 0}, nil
}

func call(t *testing.T, h *Handler, method, params string) (any, *dbxpluginsdk.PluginError) {
	t.Helper()
	if params == "" {
		params = "null"
	}
	return h.Handle(dbxpluginsdk.RequestContext{}, method, json.RawMessage(params), nil)
}

func errorCode(t *testing.T, pluginError *dbxpluginsdk.PluginError) string {
	t.Helper()
	if pluginError == nil {
		t.Fatal("expected a plugin error")
	}
	data, ok := pluginError.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected error data: %#v", pluginError.Data)
	}
	code, _ := data["code"].(string)
	return code
}

func TestUnknownMethod(t *testing.T) {
	h := New(&fakeExecutor{})
	_, pluginError := call(t, h, "unknown/method", "{}")
	if pluginError == nil || pluginError.Code != -32601 {
		t.Fatalf("expected method not found, got %#v", pluginError)
	}
}

func TestInvalidParams(t *testing.T) {
	h := New(&fakeExecutor{})
	_, pluginError := call(t, h, "wsl/distros/exec", `{"name":"Debian"}`)
	if code := errorCode(t, pluginError); code != "INVALID_PARAMS" {
		t.Fatalf("expected INVALID_PARAMS, got %q", code)
	}
	_, pluginError = call(t, h, "console/run", `{"tool":"bash","args":[]}`)
	if code := errorCode(t, pluginError); code != "INVALID_PARAMS" {
		t.Fatalf("expected INVALID_PARAMS, got %q", code)
	}
	_, pluginError = call(t, h, "jobs/get", `{"jobId":""}`)
	if code := errorCode(t, pluginError); code != "INVALID_PARAMS" {
		t.Fatalf("expected INVALID_PARAMS, got %q", code)
	}
}

func TestSystemInfoDegradesGracefully(t *testing.T) {
	h := New(&fakeExecutor{resolveErr: errors.New("missing")})
	result, pluginError := call(t, h, "system/info", "null")
	if pluginError != nil {
		t.Fatalf("system/info must not fail when tools are missing: %#v", pluginError)
	}
	payload := result.(map[string]any)
	warnings, _ := payload["warnings"].([]string)
	if len(warnings) != 2 {
		t.Fatalf("expected two warnings, got %#v", payload["warnings"])
	}
	wslBlock := payload["wsl"].(map[string]any)
	if wslBlock["available"] != false {
		t.Fatalf("expected wsl unavailable, got %#v", wslBlock)
	}
	wslcBlock := payload["wslc"].(map[string]any)
	if wslcBlock["available"] != false {
		t.Fatalf("expected wslc unavailable, got %#v", wslcBlock)
	}
}

func TestDistroExecRunResult(t *testing.T) {
	fake := &fakeExecutor{run: func(_ context.Context, request execx.Request) (execx.Result, error) {
		if strings.Contains(strings.Join(request.Args, " "), "sh -lc uname -r") {
			return execx.Result{ExitCode: 0, Stdout: "6.18.54.1\n", Command: "wsl -d Debian -e sh -lc 'uname -r'"}, nil
		}
		return execx.Result{ExitCode: 0}, nil
	}}
	h := New(fake)
	result, pluginError := call(t, h, "wsl/distros/exec", `{"name":"Debian","command":"uname -r"}`)
	if pluginError != nil {
		t.Fatalf("unexpected error: %#v", pluginError)
	}
	payload := result.(map[string]any)
	if payload["exitCode"] != 0 || payload["stdout"] != "6.18.54.1\n" {
		t.Fatalf("unexpected exec result: %#v", payload)
	}
	if payload["name"] != "Debian" || payload["started"] != true {
		t.Fatalf("unexpected exec result: %#v", payload)
	}
}

func TestDistroListMerge(t *testing.T) {
	fake := &fakeExecutor{run: func(_ context.Context, request execx.Request) (execx.Result, error) {
		joined := strings.Join(request.Args, " ")
		if strings.Contains(joined, "--list --verbose") {
			return execx.Result{Stdout: "  NAME      STATE           VERSION\n* Debian    Stopped         2\n"}, nil
		}
		return execx.Result{}, nil
	}}
	h := New(fake)
	result, pluginError := call(t, h, "wsl/distros/list", "null")
	if pluginError != nil {
		t.Fatalf("unexpected error: %#v", pluginError)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Distros []struct {
			Name      string `json:"name"`
			IsDefault bool   `json:"isDefault"`
			Version   int    `json:"version"`
		} `json:"distros"`
		DefaultDistro  string `json:"defaultDistro"`
		DefaultVersion int    `json:"defaultVersion"`
		Count          int    `json:"count"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 || len(payload.Distros) != 1 {
		t.Fatalf("unexpected payload: %s", string(raw))
	}
	if payload.Distros[0].Name != "Debian" || !payload.Distros[0].IsDefault || payload.Distros[0].Version != 2 {
		t.Fatalf("unexpected distro: %s", string(raw))
	}
	if payload.DefaultDistro != "Debian" || payload.DefaultVersion != 2 {
		t.Fatalf("unexpected default: %s", string(raw))
	}
}

func TestWslcNetworksList(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "networks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeExecutor{run: func(_ context.Context, request execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: string(raw)}, nil
	}}
	h := New(fake)
	result, pluginError := call(t, h, "wslc/networks/list", "{}")
	if pluginError != nil {
		t.Fatalf("unexpected error: %#v", pluginError)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Networks []struct {
			Name string `json:"name"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Networks) != 4 {
		t.Fatalf("expected 4 networks, got %d (%s)", len(payload.Networks), string(encoded))
	}
}

func TestInspectNotFound(t *testing.T) {
	fake := &fakeExecutor{run: func(_ context.Context, request execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stdout: "[]", Stderr: "找不到对象: nope"}, nil
	}}
	h := New(fake)
	_, pluginError := call(t, h, "wslc/containers/inspect", `{"id":"nope"}`)
	if code := errorCode(t, pluginError); code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %q", code)
	}
}

func TestConsoleRun(t *testing.T) {
	fake := &fakeExecutor{run: func(_ context.Context, request execx.Request) (execx.Result, error) {
		if request.Tool != execx.ToolWSL {
			t.Fatalf("unexpected tool: %v", request.Tool)
		}
		return execx.Result{ExitCode: 0, Stdout: "  NAME STATE VERSION\n* Debian Stopped 2\n", Command: "wsl -l -v"}, nil
	}}
	h := New(fake)
	result, pluginError := call(t, h, "console/run", `{"tool":"wsl","args":["-l","-v"]}`)
	if pluginError != nil {
		t.Fatalf("unexpected error: %#v", pluginError)
	}
	payload := result.(map[string]any)
	if payload["exitCode"] != 0 || payload["tool"] != "wsl" {
		t.Fatalf("unexpected console result: %#v", payload)
	}
	presets, pluginError := call(t, h, "console/presets", "null")
	if pluginError != nil || len(presets.(map[string]any)["presets"].([]map[string]any)) == 0 {
		t.Fatalf("unexpected presets: %#v %#v", presets, pluginError)
	}
}

func TestJobsQueries(t *testing.T) {
	h := New(&fakeExecutor{})
	result, pluginError := call(t, h, "jobs/list", "{}")
	if pluginError != nil {
		t.Fatalf("unexpected error: %#v", pluginError)
	}
	jobs := result.(map[string]any)["jobs"].([]map[string]any)
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs, got %#v", jobs)
	}
	_, pluginError = call(t, h, "jobs/get", `{"jobId":"job-1"}`)
	if code := errorCode(t, pluginError); code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %q", code)
	}
}

func TestWslcInfo(t *testing.T) {
	fake := &fakeExecutor{run: func(_ context.Context, request execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: `{"Client":{"Version":"3.0.3.0","KernelVersion":"6.18.54.1-1","SettingsFile":"C:\\x\\settings.yaml"},"Server":{"SessionManagerVersion":"3.0.3","Sessions":[{"CreatorPid":1,"ID":1,"Name":"s"}]}}`}, nil
	}}
	h := New(fake)
	result, pluginError := call(t, h, "wslc/info", "null")
	if pluginError != nil {
		t.Fatalf("unexpected error: %#v", pluginError)
	}
	raw, _ := json.Marshal(result)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["version"] != "3.0.3.0" {
		t.Fatalf("unexpected info: %s", string(raw))
	}
	server := payload["server"].(map[string]any)
	if server["sessionManagerVersion"] != "3.0.3" {
		t.Fatalf("unexpected server: %s", string(raw))
	}
}

func TestRegistryLoginRoutesSecretThroughStdin(t *testing.T) {
	const password = "route-secret-42"
	calls := []execx.Request{}
	fake := &fakeExecutor{run: func(_ context.Context, request execx.Request) (execx.Result, error) {
		calls = append(calls, request)
		return execx.Result{ExitCode: 0, Stdout: "Login Succeeded\n"}, nil
	}}
	h := New(fake)
	result, pluginError := call(t, h, "wslc/registry/login", `{"server":"ghcr.io","username":"alice","password":"`+password+`"}`)
	if pluginError != nil {
		t.Fatalf("unexpected error: %#v", pluginError)
	}
	payload := result.(map[string]any)
	if payload["server"] != "ghcr.io" || payload["username"] != "alice" || payload["loggedIn"] != true {
		t.Fatalf("unexpected result: %#v", payload)
	}
	if len(calls) != 1 || string(calls[0].Stdin) != password+"\n" {
		t.Fatalf("password must go through stdin: %#v", calls)
	}
	for _, argument := range calls[0].Args {
		if strings.Contains(argument, password) {
			t.Fatalf("password leaked into args: %#v", calls[0].Args)
		}
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), password) {
		t.Fatalf("password leaked into response: %s", string(encoded))
	}
}

func TestRegistryLoginDoesNotEchoPasswordInErrors(t *testing.T) {
	const marker = "supersecret-marker-value"
	h := New(&fakeExecutor{})
	_, pluginError := call(t, h, "wslc/registry/login", `{"username":"alice","password":{"nested":"`+marker+`"}}`)
	if code := errorCode(t, pluginError); code != "INVALID_PARAMS" {
		t.Fatalf("expected INVALID_PARAMS, got %q", code)
	}
	encoded, _ := json.Marshal(pluginError)
	if strings.Contains(string(encoded), marker) {
		t.Fatalf("password leaked into error: %s", string(encoded))
	}
	_, pluginError = call(t, h, "wslc/registry/login", `{"server":"docker.io","username":"","password":""}`)
	if code := errorCode(t, pluginError); code != "INVALID_PARAMS" {
		t.Fatalf("expected INVALID_PARAMS, got %q", code)
	}
}

func TestRegistryLogoutRoute(t *testing.T) {
	fake := &fakeExecutor{run: func(_ context.Context, _ execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: "未登录到 registry.example.com\n错误代码： ERROR_NOT_FOUND\n"}, nil
	}}
	h := New(fake)
	result, pluginError := call(t, h, "wslc/registry/logout", `{"server":"registry.example.com"}`)
	if pluginError != nil {
		t.Fatalf("idempotent logout must succeed: %#v", pluginError)
	}
	payload := result.(map[string]any)
	if payload["loggedOut"] != true || payload["server"] != "registry.example.com" {
		t.Fatalf("unexpected result: %#v", payload)
	}
}
