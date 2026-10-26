package handler

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/w89588/dbx-plugin-wsl/internal/execx"
)

func TestTemplateLifecycleAndSecretBoundaries(t *testing.T) {
	for _, mode := range []string{"success", "run-failure", "pull-timeout", "cancel", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("LOCALAPPDATA", dir)
			listener, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			var mu sync.Mutex
			volume, container := false, false
			token := ""
			runCount := 0
			envPath := ""
			secret := "sensitive-test-password"
			pullStarted := make(chan struct{})
			h := New(&fakeExecutor{run: func(ctx context.Context, r execx.Request) (execx.Result, error) {
				mu.Lock()
				defer mu.Unlock()
				if strings.Contains(strings.Join(r.Args, " "), secret) || strings.Contains(r.Display, secret) {
					t.Error("secret in command arguments")
				}
				a := r.Args
				if len(a) > 1 && a[1] == "inspect" {
					exists := volume
					if a[0] == "container" {
						exists = container
					}
					if mode == "foreign" {
						exists = true
					}
					if !exists {
						return execx.Result{ExitCode: 1, Stderr: "not found"}, nil
					}
					b, _ := json.Marshal([]any{map[string]any{"Labels": map[string]string{"com.dbx.wsl.owner": token}, "State": map[string]any{"Running": true, "Status": "running", "Health": map[string]string{"Status": "healthy"}}}})
					return execx.Result{Stdout: string(b)}, nil
				}
				if a[0] == "pull" {
					if mode == "pull-timeout" {
						return execx.Result{TimedOut: true, Stderr: secret}, nil
					}
					if mode == "cancel" {
						close(pullStarted)
						<-ctx.Done()
						return execx.Result{Canceled: true, ExitCode: -1, Stderr: secret}, ctx.Err()
					}
					return execx.Result{}, nil
				}
				if a[0] == "volume" && a[1] == "create" {
					volume = true
					token = strings.TrimPrefix(a[3], "com.dbx.wsl.owner=")
					return execx.Result{}, nil
				}
				if a[0] == "run" {
					runCount++
					for i, arg := range a {
						if arg == "--env-file" {
							envPath = a[i+1]
							b, e := os.ReadFile(envPath)
							if e != nil || !strings.Contains(string(b), secret) {
								t.Error("missing credential file")
							}
						}
					}
					if mode == "run-failure" {
						return execx.Result{ExitCode: 1, Stderr: secret}, nil
					}
					container = true
					return execx.Result{}, nil
				}
				t.Errorf("unexpected command %v", a)
				return execx.Result{}, nil
			}})
			p := map[string]any{"kind": "redis", "name": "test-env", "port": port, "password": secret}
			b, _ := json.Marshal(p)
			value, err := call(t, h, "templates/create", string(b))
			if mode == "foreign" {
				if err == nil {
					t.Fatal("foreign resource accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				<-pullStarted
				h.Jobs.Cancel(value.(map[string]any)["jobId"].(string))
			}
			done := awaitJob(t, h, value)
			data, _ := json.Marshal(done)
			if strings.Contains(string(data), secret) {
				t.Fatal("secret in job output")
			}
			mu.Lock()
			defer mu.Unlock()
			if envPath != "" {
				if _, e := os.Stat(envPath); !os.IsNotExist(e) {
					t.Fatal("temporary credential retained")
				}
			}
			record, e := os.ReadFile(filepath.Join(dir, "dbx-wsl-manager", "environments", "test-env.json"))
			if e != nil || strings.Contains(string(record), secret) {
				t.Fatal("invalid persisted record")
			}
			if mode != "success" {
				if done["status"] == "completed" {
					t.Fatal(done)
				}
				if mode == "run-failure" && !volume {
					t.Fatal("failure removed volume")
				}
				return
			}
			if done["status"] != "completed" {
				t.Fatal(done)
			}
			mu.Unlock()
			reused, err := call(t, h, "templates/create", string(b))
			mu.Lock()
			if err != nil || reused.(map[string]any)["reused"] != true || runCount != 1 {
				t.Fatal("duplicate was not reused", err, reused)
			}
			p["port"] = port%65535 + 1
			b, _ = json.Marshal(p)
			mu.Unlock()
			_, err = call(t, h, "templates/create", string(b))
			mu.Lock()
			if err == nil {
				t.Fatal("conflicting spec accepted")
			}
		})
	}
}

func TestMCPSemanticContracts(t *testing.T) {
	h := New(&fakeExecutor{run: func(context.Context, execx.Request) (execx.Result, error) {
		t.Fatal("describe executed a command")
		return execx.Result{}, nil
	}})
	_, out := mcpInvoke(t, h, "wsl_describe", "templates/create", nil)
	m := out["methods"].([]any)[0].(map[string]any)
	if m["retrySafety"] != "inspect-before-retry" {
		t.Fatal(m)
	}
	schema := m["inputSchema"].(map[string]any)
	if len(schema["required"].([]any)) != 4 {
		t.Fatal(schema)
	}
	if schema["properties"].(map[string]any)["password"].(map[string]any)["writeOnly"] != true {
		t.Fatal(schema)
	}
	envelope, _ := mcpInvoke(t, h, "wsl_read", "templates/create", map[string]any{"kind": "redis", "name": "abc", "port": 6379, "password": "test-password"})
	if envelope["isError"] != true {
		t.Fatal("write through read tool")
	}
	for _, entry := range mcpMethods {
		d := describeMethod(entry)
		schema := d["inputSchema"].(map[string]any)
		if required, ok := schema["required"].([]string); ok {
			for _, key := range required {
				if schema["properties"].(map[string]any)[key] == nil {
					t.Fatalf("%s invalid required %s", entry.Method, key)
				}
			}
		}
	}
}
