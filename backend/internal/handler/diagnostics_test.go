package handler

import (
	"context"
	"encoding/json"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMCPDiagnosticsRequireWriteAuthorization(t *testing.T) {
	// No requests leave the process, even while reproducing the old behavior.
	requests, commands := 0, 0
	previous := http.DefaultTransport
	http.DefaultTransport = diagnosticTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/v2/" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected diagnostic request: %v", r)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	h := New(&fakeExecutor{run: func(context.Context, execx.Request) (execx.Result, error) { commands++; return execx.Result{}, nil }})
	for _, p := range []map[string]any{
		{},
		{"registry": "registry.example:8443"},
		{"registry": "127.0.0.1:8443"},
		{"registry": "[::1]:8443"},
		{"ports": []int{5432}},
		{"registry": "registry.example:8443", "ports": []int{5432}},
	} {
		env, payload := mcpInvoke(t, h, "wsl_read", "diagnostics/run", p)
		if env["isError"] != true {
			t.Errorf("read-only diagnostics accepted: %v", p)
		} else if payload["error"].(map[string]any)["code"] != "INVALID_PARAMS" {
			t.Errorf("unexpected error: %v", payload)
		}
	}
	if commands != 0 || requests != 0 {
		t.Errorf("read-only path reached diagnostics: commands=%d requests=%d", commands, requests)
	}
	_, description := mcpInvoke(t, h, "wsl_describe", "diagnostics/run", nil)
	if description["methods"].([]any)[0].(map[string]any)["readOnly"] != false {
		t.Error("diagnostics advertised as read-only")
	}
	// The approved write surface retains both registry and port diagnostics.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	beforeRequests := requests
	env, report := mcpInvoke(t, h, "wsl_write", "diagnostics/run", map[string]any{"registry": "registry.example:8443", "ports": []int{port}})
	if env["isError"] != false || report["redacted"] != true || requests != beforeRequests+1 {
		t.Fatalf("authorized diagnostics failed: %v", report)
	}
	registry, busyPort := false, false
	for _, raw := range report["checks"].([]any) {
		check := raw.(map[string]any)
		if check["id"] == "registry" && check["status"] == "ok" {
			registry = true
		}
		if check["id"] == "port-"+strconv.Itoa(port) && check["status"] == "warning" {
			busyPort = true
		}
	}
	if !registry || !busyPort {
		t.Fatalf("authorized checks missing: %v", report)
	}
}

func TestDiagnosticReportRedactsRawOutputAndFindsBusyPort(t *testing.T) {
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	h := New(&fakeExecutor{run: func(context.Context, execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: "password=private-secret C:/Users/private-user"}, nil
	}})
	report, err := h.diagnostics(context.Background(), diagnosticParams{Ports: []int{port}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report)
	for _, secret := range []string{"private-secret", "private-user"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal(string(raw))
		}
	}
	checks := report.(map[string]any)["checks"].([]diagnosticCheck)
	found := false
	for _, c := range checks {
		if c.ID == "port-"+strconv.Itoa(port) && c.Status == "warning" {
			found = true
		}
	}
	if !found {
		t.Fatal(checks)
	}
	for _, host := range []string{"https://example.com", "user:password@registry.example", "registry.example/path", "registry.example\n"} {
		if _, e := registryURL(host); e == nil {
			t.Fatalf("accepted invalid host %q", host)
		}
	}
}
