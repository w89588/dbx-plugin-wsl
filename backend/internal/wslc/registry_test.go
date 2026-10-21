package wslc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
)

const testPassword = "s3cr3t-pw-do-not-log"

func waitJobFinished(t *testing.T, manager *jobs.Manager, jobID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, ok := manager.Get(jobID)
		if !ok {
			t.Fatalf("job %s not found", jobID)
		}
		if snapshot["status"] != string(jobs.StatusRunning) {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish", jobID)
	return nil
}

func jobErrorCode(snapshot map[string]any) string {
	payload, ok := snapshot["error"].(map[string]any)
	if !ok {
		return ""
	}
	code, _ := payload["code"].(string)
	return code
}

func TestRegistryLoginUsesStdinOnly(t *testing.T) {
	calls := []execx.Request{}
	service := New(&fakeExecutor{
		calls: &calls,
		run: func(_ context.Context, _ execx.Request) (execx.Result, error) {
			return execx.Result{ExitCode: 0, Stdout: "Login Succeeded\n"}, nil
		},
	}, jobs.NewManager())

	result, appErr := service.RegistryLogin(context.Background(), LoginParams{
		Server:   "docker.io",
		Username: "alice",
		Password: testPassword,
	})
	if appErr != nil {
		t.Fatalf("unexpected error: %#v", appErr)
	}
	payload := result.(map[string]any)
	if payload["server"] != "docker.io" || payload["username"] != "alice" || payload["loggedIn"] != true || payload["output"] != "Login Succeeded" {
		t.Fatalf("unexpected result: %#v", payload)
	}
	if len(calls) != 1 {
		t.Fatalf("expected one command, got %#v", calls)
	}
	request := calls[0]
	wantArgs := []string{"login", "docker.io", "-u", "alice", "--password-stdin"}
	if strings.Join(request.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("unexpected args: %#v", request.Args)
	}
	for _, argument := range request.Args {
		if strings.Contains(argument, testPassword) {
			t.Fatalf("password leaked into argv: %#v", request.Args)
		}
	}
	if string(request.Stdin) != testPassword+"\n" {
		t.Fatalf("unexpected stdin: %q", string(request.Stdin))
	}
	if request.Tool != execx.ToolWSLc {
		t.Fatalf("unexpected tool: %v", request.Tool)
	}
}

func TestRegistryLoginDefaultServer(t *testing.T) {
	calls := []execx.Request{}
	service := New(&fakeExecutor{calls: &calls}, jobs.NewManager())
	result, appErr := service.RegistryLogin(context.Background(), LoginParams{Username: "alice", Password: testPassword})
	if appErr != nil {
		t.Fatalf("unexpected error: %#v", appErr)
	}
	if result.(map[string]any)["server"] != "docker.io" {
		t.Fatalf("unexpected server: %#v", result)
	}
	wantArgs := []string{"login", "-u", "alice", "--password-stdin"}
	if strings.Join(calls[0].Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("unexpected args: %#v", calls[0].Args)
	}
}

func TestRegistryLoginAuthFailedRedactsPassword(t *testing.T) {
	service := New(&fakeExecutor{
		run: func(_ context.Context, _ execx.Request) (execx.Result, error) {
			return execx.Result{
				ExitCode: 1,
				Stderr: "来自引擎的错误信息: Get \"https://registry-1.docker.io/v2/\": " +
					"unauthorized: incorrect username or password (" + testPassword + ")\n错误代码： E_FAIL\n",
			}, nil
		},
	}, jobs.NewManager())
	_, appErr := service.RegistryLogin(context.Background(), LoginParams{Username: "alice", Password: testPassword})
	if appErr == nil || appErr.Code != apperr.CodeRegistryAuthFailed {
		t.Fatalf("expected REGISTRY_AUTH_FAILED, got %#v", appErr)
	}
	combined := appErr.Message + "\n" + appErr.Hint + "\n" + appErr.Detail
	if strings.Contains(combined, testPassword) {
		t.Fatalf("password leaked into error: %#v", appErr)
	}
	if !strings.Contains(appErr.Detail, "***") {
		t.Fatalf("expected redacted detail, got %q", appErr.Detail)
	}
	if strings.Contains(appErr.Detail, "wslc login") || strings.Contains(appErr.Detail, "--password-stdin") {
		t.Fatalf("full command must not be in detail: %q", appErr.Detail)
	}
}

func TestRegistryLoginValidation(t *testing.T) {
	cases := []struct {
		name   string
		params LoginParams
	}{
		{"empty username", LoginParams{Password: "x"}},
		{"blank username", LoginParams{Username: "   ", Password: "x"}},
		{"empty password", LoginParams{Username: "alice"}},
		{"server with space", LoginParams{Server: "docker.io extra", Username: "alice", Password: "x"}},
		{"server with tab", LoginParams{Server: "docker.io\tx", Username: "alice", Password: "x"}},
		{"server leading dash", LoginParams{Server: "-evil", Username: "alice", Password: "x"}},
		{"server too long", LoginParams{Server: strings.Repeat("a", 256), Username: "alice", Password: "x"}},
		{"password with newline", LoginParams{Username: "alice", Password: "a\nb"}},
		{"password with cr", LoginParams{Username: "alice", Password: "a\rb"}},
		{"username with space", LoginParams{Username: "ali ce", Password: "x"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			calls := []execx.Request{}
			service := New(&fakeExecutor{calls: &calls}, jobs.NewManager())
			_, appErr := service.RegistryLogin(context.Background(), testCase.params)
			if appErr == nil || appErr.Code != apperr.CodeInvalidParams {
				t.Fatalf("expected INVALID_PARAMS, got %#v", appErr)
			}
			if len(calls) != 0 {
				t.Fatalf("no command must run for invalid params: %#v", calls)
			}
		})
	}
}

func TestRegistryLogoutIdempotent(t *testing.T) {
	calls := []execx.Request{}
	service := New(&fakeExecutor{
		calls: &calls,
		run: func(_ context.Context, _ execx.Request) (execx.Result, error) {
			return execx.Result{ExitCode: 1, Stderr: "未登录到 registry.example.com\n错误代码： ERROR_NOT_FOUND\n"}, nil
		},
	}, jobs.NewManager())
	result, appErr := service.RegistryLogout(context.Background(), LogoutParams{Server: "registry.example.com"})
	if appErr != nil {
		t.Fatalf("idempotent logout must not fail: %#v", appErr)
	}
	payload := result.(map[string]any)
	if payload["server"] != "registry.example.com" || payload["loggedOut"] != true {
		t.Fatalf("unexpected result: %#v", payload)
	}
	if !strings.Contains(payload["output"].(string), "未登录到") {
		t.Fatalf("unexpected output: %#v", payload["output"])
	}
	if strings.Join(calls[0].Args, " ") != "logout registry.example.com" {
		t.Fatalf("unexpected args: %#v", calls[0].Args)
	}
}

func TestRegistryLogoutSuccess(t *testing.T) {
	service := New(&fakeExecutor{
		run: func(_ context.Context, _ execx.Request) (execx.Result, error) {
			return execx.Result{ExitCode: 0, Stdout: "Removing login credentials for https://index.docker.io/v1/\n"}, nil
		},
	}, jobs.NewManager())
	result, appErr := service.RegistryLogout(context.Background(), LogoutParams{})
	if appErr != nil {
		t.Fatalf("unexpected error: %#v", appErr)
	}
	payload := result.(map[string]any)
	if payload["server"] != "docker.io" || payload["loggedOut"] != true {
		t.Fatalf("unexpected result: %#v", payload)
	}
	if !strings.Contains(payload["output"].(string), "Removing login credentials") {
		t.Fatalf("unexpected output: %#v", payload["output"])
	}
}

func TestClassifyRegistryFailureTable(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		stderr string
		code   string
	}{
		{
			name:   "auth required wins over image not found",
			stderr: "来自引擎的错误信息: pull access denied for definitely-not-a-real-image-dbx-xyz, repository does not exist or may require 'docker login': denied: requested access to the resource is denied\n错误代码： WSLC_E_IMAGE_NOT_FOUND\n",
			code:   apperr.CodeRegistryAuthRequired,
		},
		{
			name:   "unauthorized",
			stderr: "unauthorized: authentication required",
			code:   apperr.CodeRegistryAuthRequired,
		},
		{
			name:   "rate limited",
			stderr: "toomanyrequests: You have reached your pull rate limit. You may increase the limit by authenticating.",
			code:   apperr.CodeRegistryRateLimited,
		},
		{
			name:   "image not found without auth keywords",
			stderr: "找不到映像 'dbx/missing:latest'。\n错误代码： WSLC_E_IMAGE_NOT_FOUND",
			code:   apperr.CodeNotFound,
		},
		{
			name:   "other failure",
			stderr: "internal error: failed to resolve source metadata",
			code:   apperr.CodeProcessFailed,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			appErr := classifyRegistryFailure("拉取镜像失败", testCase.stdout, testCase.stderr)
			if appErr.Code != testCase.code {
				t.Fatalf("expected %s, got %#v", testCase.code, appErr)
			}
			if testCase.code == apperr.CodeRegistryAuthRequired && appErr.Hint != apperr.HintRegistryAuthRequired {
				t.Fatalf("unexpected hint: %q", appErr.Hint)
			}
			if testCase.code == apperr.CodeRegistryRateLimited && appErr.Hint != apperr.HintRegistryRateLimited {
				t.Fatalf("unexpected hint: %q", appErr.Hint)
			}
			if appErr.Detail == "" || !strings.Contains(appErr.Detail, strings.Split(strings.TrimSpace(testCase.stderr), "\n")[0]) {
				t.Fatalf("expected raw detail, got %q", appErr.Detail)
			}
		})
	}
}

func TestImagesPullJobClassifiesAuthFailure(t *testing.T) {
	streamRequests := []execx.Request{}
	stderr := "来自引擎的错误信息: pull access denied for definitely-not-a-real-image-dbx-xyz, repository does not exist or may require 'docker login': denied: requested access to the resource is denied\n错误代码： WSLC_E_IMAGE_NOT_FOUND\n"
	fake := &fakeExecutor{
		streams: &streamRequests,
		stream: func(_ context.Context, _ execx.Request, onLine execx.LineFunc) (execx.StreamResult, error) {
			for _, line := range strings.SplitAfter(stderr, "\n") {
				if line != "" {
					onLine("stderr", line)
				}
			}
			return execx.StreamResult{ExitCode: 1, Stderr: stderr}, nil
		},
	}
	manager := jobs.NewManager()
	service := New(fake, manager)
	result, appErr := service.ImagesPull(context.Background(), nil, PullParams{Ref: "docker.io/library/definitely-not-a-real-image-dbx-xyz:latest"})
	if appErr != nil {
		t.Fatalf("starting the job must succeed: %#v", appErr)
	}
	jobID := result.(map[string]any)["jobId"].(string)
	snapshot := waitJobFinished(t, manager, jobID)
	if snapshot["status"] != string(jobs.StatusFailed) {
		t.Fatalf("expected failed job, got %#v", snapshot["status"])
	}
	if code := jobErrorCode(snapshot); code != apperr.CodeRegistryAuthRequired {
		t.Fatalf("expected REGISTRY_AUTH_REQUIRED, got %q (%#v)", code, snapshot["error"])
	}
	lines, _ := snapshot["lines"].([]jobs.Line)
	if len(lines) == 0 {
		t.Fatal("job/output must keep the raw wslc error")
	}
	joined := ""
	for _, line := range lines {
		joined += line.Text
	}
	if !strings.Contains(joined, "may require 'docker login'") {
		t.Fatalf("raw job output was not preserved: %q", joined)
	}
	if len(streamRequests) != 1 || strings.Join(streamRequests[0].Args, " ") != "pull docker.io/library/definitely-not-a-real-image-dbx-xyz:latest" {
		t.Fatalf("unexpected pull args: %#v", streamRequests)
	}
}

func TestContainersRunClassifiesPullAuthFailure(t *testing.T) {
	service := New(&fakeExecutor{
		run: func(_ context.Context, _ execx.Request) (execx.Result, error) {
			return execx.Result{
				ExitCode: 1,
				Stderr:   "找不到映像 'docker.io/library/dbx-missing:latest'，正在拉取\n来自引擎的错误信息: pull access denied for dbx-missing, repository does not exist or may require 'docker login'\n错误代码： WSLC_E_IMAGE_NOT_FOUND\n",
			}, nil
		},
	}, jobs.NewManager())
	_, appErr := service.ContainersRun(context.Background(), RunParams{Image: "docker.io/library/dbx-missing:latest"})
	if appErr == nil || appErr.Code != apperr.CodeRegistryAuthRequired {
		t.Fatalf("expected REGISTRY_AUTH_REQUIRED, got %#v", appErr)
	}
}

func TestSanitizeSecret(t *testing.T) {
	if got := sanitizeSecret("password="+testPassword+" end", testPassword); strings.Contains(got, testPassword) || !strings.Contains(got, "***") {
		t.Fatalf("unexpected redaction: %q", got)
	}
	if got := sanitizeSecret("no secret here"); got != "no secret here" {
		t.Fatalf("unexpected redaction: %q", got)
	}
	if got := sanitizeSecret("a-b a-b", "a-b"); got != "*** ***" {
		t.Fatalf("unexpected redaction: %q", got)
	}
}
