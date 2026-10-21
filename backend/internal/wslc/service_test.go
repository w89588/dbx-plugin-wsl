package wslc

import (
	"context"
	"strings"
	"testing"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
)

type fakeExecutor struct {
	run     func(ctx context.Context, request execx.Request) (execx.Result, error)
	stream  func(ctx context.Context, request execx.Request, onLine execx.LineFunc) (execx.StreamResult, error)
	calls   *[]execx.Request
	streams *[]execx.Request
}

func (f *fakeExecutor) Resolve(_ execx.Tool) (string, error) { return `C:\Fake\wslc.exe`, nil }

func (f *fakeExecutor) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, request)
	}
	if f.run != nil {
		return f.run(ctx, request)
	}
	return execx.Result{ExitCode: 0}, nil
}

func (f *fakeExecutor) Stream(ctx context.Context, request execx.Request, onLine execx.LineFunc) (execx.StreamResult, error) {
	if f.streams != nil {
		*f.streams = append(*f.streams, request)
	}
	if f.stream != nil {
		return f.stream(ctx, request, onLine)
	}
	return execx.StreamResult{ExitCode: 0}, nil
}

func TestContainersExecNonZeroIsRunResult(t *testing.T) {
	service := New(&fakeExecutor{run: func(_ context.Context, _ execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: "cat: /nope: No such file or directory\n"}, nil
	}}, jobs.NewManager())
	result, appErr := service.ContainersExec(context.Background(), ExecParams{ID: "demo", Command: []string{"cat", "/nope"}})
	if appErr != nil {
		t.Fatalf("container command failure must not be an RPC error: %#v", appErr)
	}
	payload := result.(map[string]any)
	if payload["exitCode"] != 1 || !strings.Contains(payload["stderr"].(string), "No such file") {
		t.Fatalf("unexpected result: %#v", payload)
	}
}

func TestContainersExecMissingContainerIsError(t *testing.T) {
	service := New(&fakeExecutor{run: func(_ context.Context, _ execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: "找不到容器 'demo'。\r\n错误代码： WSLC_E_CONTAINER_NOT_FOUND\r\n"}, nil
	}}, jobs.NewManager())
	_, appErr := service.ContainersExec(context.Background(), ExecParams{ID: "demo", Command: []string{"echo", "hi"}})
	if appErr == nil || appErr.Code != apperr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %#v", appErr)
	}
}

func TestNetworksCreateIPv6Unsupported(t *testing.T) {
	calls := []execx.Request{}
	service := New(&fakeExecutor{calls: &calls}, jobs.NewManager())
	_, appErr := service.NetworksCreate(context.Background(), CreateNetworkParams{Name: "dbxnet", IPv6: true})
	if appErr == nil || appErr.Code != apperr.CodeUnsupported {
		t.Fatalf("expected UNSUPPORTED, got %#v", appErr)
	}
	if len(calls) != 0 {
		t.Fatalf("no command must run: %#v", calls)
	}
}

func TestFailureClassification(t *testing.T) {
	notFound := failure("失败", "", "找不到容器 'x'。\r\n错误代码： WSLC_E_CONTAINER_NOT_FOUND\r\n")
	if notFound.Code != apperr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %#v", notFound)
	}
	conflict := failure("失败", "", "容器正在运行，无法删除")
	if conflict.Code != apperr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %#v", conflict)
	}
	permission := failure("失败", "", "拒绝访问。\r\n错误代码： E_ACCESSDENIED\r\n")
	if permission.Code != apperr.CodeProcessFailed || !strings.Contains(permission.Message, "权限") {
		t.Fatalf("expected permission failure, got %#v", permission)
	}
	general := failure("任务失败", "stdout text", "")
	if general.Code != apperr.CodeProcessFailed || general.Detail != "stdout text" {
		t.Fatalf("expected PROCESS_FAILED with stdout detail, got %#v", general)
	}
}

func TestSortedEnv(t *testing.T) {
	pairs := sortedEnv(map[string]string{"B": "2", "A": "1"})
	if len(pairs) != 2 || pairs[0] != "A=1" || pairs[1] != "B=2" {
		t.Fatalf("unexpected env order: %#v", pairs)
	}
}

func TestRequireHelpers(t *testing.T) {
	if _, appErr := requireID("  "); appErr == nil || appErr.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected INVALID_PARAMS for empty id")
	}
	if values, appErr := requireList("refs", []string{" a ", "b"}); appErr != nil || len(values) != 2 || values[0] != "a" {
		t.Fatalf("unexpected list cleaning: %#v %#v", values, appErr)
	}
	if _, appErr := requireList("refs", nil); appErr == nil || appErr.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected INVALID_PARAMS for empty list")
	}
}
