package handler

import (
	"context"
	"encoding/json"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"net"
	"strconv"
	"strings"
	"testing"
)

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
