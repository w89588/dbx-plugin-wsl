package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
)

func TestResourceLeaseOverHostProtocol(t *testing.T) {
	h := New(&fakeExecutor{})
	input, inputWriter := io.Pipe()
	output, outputWriter := io.Pipe()
	defer inputWriter.Close()
	defer output.Close()
	defer func() {
		h.streamsMu.Lock()
		for _, stream := range h.streams {
			stream.cancel()
		}
		h.streamsMu.Unlock()
	}()
	server := sdk.NewServer(sdk.Metadata{ID: "com.dbx.wsl", Version: "0.1.5"}, h).WithIO(input, outputWriter, io.Discard)
	done := make(chan error, 1)
	go func() { done <- server.Serve(); outputWriter.Close() }()
	messages := make(chan map[string]any, 32)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			var msg map[string]any
			if json.Unmarshal(scanner.Bytes(), &msg) == nil {
				messages <- msg
			}
		}
	}()
	send := func(id int, method string, params any) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := fmt.Fprintln(inputWriter, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	receive := func(predicate func(map[string]any) bool) map[string]any {
		t.Helper()
		timeout := time.After(3 * time.Second)
		for {
			select {
			case msg := <-messages:
				if predicate(msg) {
					return msg
				}
			case <-timeout:
				t.Fatal("host message timeout")
				return nil
			}
		}
	}
	request := resourceRequest{ID: "protocol-view", Resources: []string{"images"}, Interval: 2}
	send(1, "resources/subscribe", request)
	response := receive(func(msg map[string]any) bool { return msg["id"] == float64(1) })
	result, ok := response["result"].(map[string]any)
	if !ok || result["generation"] == "" {
		t.Fatal(response)
	}
	generation := result["generation"]
	send(2, "resources/subscribe", request)
	response = receive(func(msg map[string]any) bool { return msg["id"] == float64(2) })
	if response["result"].(map[string]any)["generation"] != generation {
		t.Fatal("renewal recreated stream")
	}
	h.streamsMu.Lock()
	count := len(h.streams)
	h.streamsMu.Unlock()
	if count != 1 {
		t.Fatal("renewal leaked stream")
	}
	send(3, "resources/unsubscribe", map[string]any{"subscriptionId": request.ID})
	receive(func(msg map[string]any) bool { return msg["id"] == float64(3) })
	send(4, "resources/subscribe", request)
	response = receive(func(msg map[string]any) bool { return msg["id"] == float64(4) })
	if response["result"].(map[string]any)["generation"] == generation {
		t.Fatal("new stream reused generation")
	}
	h.streamsMu.Lock()
	h.streams[request.ID].expires = time.Now().Add(-time.Second)
	h.streamsMu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.streamsMu.Lock()
		count = len(h.streams)
		h.streamsMu.Unlock()
		if count == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if count != 0 {
		t.Fatal("expired lease retained")
	}
	inputWriter.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestResourceStreamChangesOnlyAndCancellation(t *testing.T) {
	var changed atomic.Bool
	var calls atomic.Int32
	h := New(&fakeExecutor{run: func(ctx context.Context, r execx.Request) (execx.Result, error) {
		calls.Add(1)
		if changed.Load() {
			return execx.Result{Stdout: `{"ID":"one","Repository":"alpine","Tag":"new"}`}, nil
		}
		return execx.Result{Stdout: `{"ID":"one","Repository":"alpine","Tag":"old"}`}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &resourceStream{cancel: cancel, expires: time.Now().Add(time.Minute)}
	h.streams["test-view"] = stream
	events := make(chan map[string]any, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.runResourceStream(ctx, resourceRequest{ID: "test-view", Resources: []string{"images"}, Interval: 1}, stream, func(method string, payload any) *sdk.PluginError {
			if method == "resources/update" {
				events <- payload.(map[string]any)
			}
			return nil
		})
	}()
	next := func() map[string]any {
		select {
		case event := <-events:
			return event
		case <-time.After(3 * time.Second):
			t.Fatal("no resource event")
			return nil
		}
	}
	first := next()
	if first["subscriptionId"] != "test-view" || first["sequence"] != uint64(1) {
		t.Fatal(first)
	}
	// Wait for a second sample to prove unchanged content does not emit again.
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatal("collector did not run again")
	}
	select {
	case event := <-events:
		t.Fatalf("duplicate snapshot: %v", event)
	case <-time.After(50 * time.Millisecond):
	}
	changed.Store(true)
	second := next()
	if second["sequence"] != uint64(2) {
		t.Fatal(second)
	}
	encoded, _ := json.Marshal(second)
	if !strings.Contains(string(encoded), "new") {
		t.Fatal(string(encoded))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop")
	}
	h.streamsMu.Lock()
	count := len(h.streams)
	h.streamsMu.Unlock()
	if count != 0 {
		t.Fatal("lease leaked")
	}
}

func TestSlowResourceDoesNotBlockOtherResources(t *testing.T) {
	stopped := make(chan struct{})
	h := New(&fakeExecutor{run: func(ctx context.Context, r execx.Request) (execx.Result, error) {
		if r.Args[0] == "list" {
			<-ctx.Done()
			close(stopped)
			return execx.Result{}, ctx.Err()
		}
		return execx.Result{Stdout: `{"ID":"one","Repository":"alpine","Tag":"latest"}`}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &resourceStream{cancel: cancel, expires: time.Now().Add(time.Minute)}
	event := make(chan map[string]any, 2)
	go h.runResourceStream(ctx, resourceRequest{ID: "slow-view", Resources: []string{"containers", "images"}, Interval: 2}, stream, func(_ string, p any) *sdk.PluginError { event <- p.(map[string]any); return nil })
	select {
	case payload := <-event:
		if payload["resources"].(map[string]any)["images"] == nil {
			t.Fatal(payload)
		}
	case <-time.After(time.Second):
		t.Fatal("slow container query blocked images")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("in-flight query not cancelled")
	}
}

func TestResourceSubscriptionValidation(t *testing.T) {
	h := New(&fakeExecutor{})
	for _, params := range []string{
		`{"subscriptionId":"test-view","resources":["console/run"],"intervalSeconds":2}`,
		`{"subscriptionId":"test-view","resources":["images","images"],"intervalSeconds":2}`,
		`{"subscriptionId":"test-view","resources":["images"],"intervalSeconds":0}`,
	} {
		if _, err := h.subscribeResources(json.RawMessage(params), nil); err == nil || err.Code != "INVALID_PARAMS" {
			t.Fatalf("accepted %s: %v", params, err)
		}
	}
}
