package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	sdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
)

// Resource streams use the existing authenticated host event transport. No HTTP
// listener or browser network permission is needed. Each view owns a leased ID.
const resourceLease = 65 * time.Second

var resourceMethods = map[string]string{
	"system": "system/info", "distros": "wsl/distros/list",
	"containers": "wslc/containers/list", "stats": "wslc/containers/stats",
	"images": "wslc/images/list", "volumes": "wslc/volumes/list", "networks": "wslc/networks/list",
}

type resourceStream struct {
	cancel     context.CancelFunc
	expires    time.Time // guarded by streamsMu
	signature  string
	generation string
}

type resourceRequest struct {
	ID        string   `json:"subscriptionId"`
	Resources []string `json:"resources"`
	Interval  int      `json:"intervalSeconds"`
}

func (h *Handler) subscribeResources(raw json.RawMessage, emitter *sdk.Emitter) (any, *apperr.Error) {
	var request resourceRequest
	if err := decodeParams(raw, &request); err != nil {
		return nil, err
	}
	if len(request.ID) < 8 || len(request.ID) > 100 || strings.ContainsAny(request.ID, "\r\n\x00") || len(request.Resources) == 0 || len(request.Resources) > len(resourceMethods) {
		return nil, apperr.InvalidParams("订阅参数不合法", "请重新打开资源页面", "")
	}
	seen := map[string]bool{}
	for _, resource := range request.Resources {
		if resourceMethods[resource] == "" || seen[resource] {
			return nil, apperr.InvalidParams("未知或重复资源", "请检查订阅资源", resource)
		}
		seen[resource] = true
	}
	if request.Interval < 2 || request.Interval > 30 {
		return nil, apperr.InvalidParams("采集间隔须为 2–30 秒", "请检查采集间隔", "")
	}
	if emitter == nil {
		return nil, apperr.Unsupported("宿主事件通道不可用", "请手动刷新资源", "")
	}
	signature := strings.Join(request.Resources, ",") + ":" + strconv.Itoa(request.Interval)
	h.streamsMu.Lock()
	defer h.streamsMu.Unlock()
	now := time.Now()
	for id, stream := range h.streams {
		if now.After(stream.expires) {
			stream.cancel()
			delete(h.streams, id)
		}
	}
	if stream := h.streams[request.ID]; stream != nil {
		if stream.signature != signature {
			return nil, apperr.Conflict("订阅内容已更改", "请使用新的订阅标识", "")
		}
		stream.expires = now.Add(resourceLease)
		return map[string]any{"subscriptionId": request.ID, "leaseSeconds": 65, "generation": stream.generation}, nil
	}
	if len(h.streams) >= 8 {
		return nil, apperr.Conflict("资源订阅过多", "请关闭不使用的工作台页面", "")
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream := &resourceStream{cancel: cancel, expires: now.Add(resourceLease), signature: signature, generation: strconv.FormatInt(now.UnixNano(), 36)}
	h.streams[request.ID] = stream
	go h.runResourceStream(ctx, request, stream, emitter.Event)
	return map[string]any{"subscriptionId": request.ID, "leaseSeconds": 65, "generation": stream.generation}, nil
}

func (h *Handler) runResourceStream(ctx context.Context, request resourceRequest, stream *resourceStream, emit func(string, any) *sdk.PluginError) {
	defer func() {
		stream.cancel()
		h.streamsMu.Lock()
		if h.streams[request.ID] == stream {
			delete(h.streams, request.ID)
		}
		h.streamsMu.Unlock()
	}()
	ticker := time.NewTicker(time.Duration(request.Interval) * time.Second)
	heartbeat := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()

	type sample struct {
		name    string
		payload map[string]any
	}
	results := make(chan sample, len(resourceMethods))
	inFlight := map[string]bool{}
	lastSample := map[string]time.Time{}
	last := map[string]string{}
	var sequence uint64
	collect := func() {
		for _, name := range request.Resources {
			if inFlight[name] {
				continue
			}
			// Version/platform discovery is expensive and changes much less often.
			if name == "system" && time.Since(lastSample[name]) < time.Minute {
				continue
			}
			inFlight[name] = true
			go func(name string) {
				sampleCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
				defer cancel()
				params := json.RawMessage(`{}`)
				if name == "containers" {
					params = json.RawMessage(`{"all":true}`)
				}
				value, err := h.dispatch(sampleCtx, resourceMethods[name], params, nil)
				payload := map[string]any{"data": value}
				if err != nil {
					payload = map[string]any{"error": map[string]any{"code": err.Code, "message": err.Message, "hint": err.Hint, "detail": err.Detail}}
				}
				select {
				case results <- sample{name, payload}:
				case <-ctx.Done():
				}
			}(name)
		}
	}
	collect()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.streamsMu.Lock()
			expired := time.Now().After(stream.expires)
			h.streamsMu.Unlock()
			if expired {
				return
			}
			collect()
		case <-heartbeat.C:
			h.streamsMu.Lock()
			expired := time.Now().After(stream.expires)
			h.streamsMu.Unlock()
			if expired {
				return
			}
			if emit("resources/heartbeat", map[string]any{"subscriptionId": request.ID, "sequence": sequence}) != nil {
				return
			}

		case result := <-results:
			inFlight[result.name] = false
			lastSample[result.name] = time.Now()
			if ctx.Err() != nil {
				return
			}
			raw, err := json.Marshal(result.payload)
			if err == nil && string(raw) != last[result.name] {
				last[result.name] = string(raw)
				sequence++
				if emit("resources/update", map[string]any{"subscriptionId": request.ID, "sequence": sequence, "resources": map[string]any{result.name: result.payload}, "sampledAt": time.Now().UnixMilli()}) != nil {
					return
				}
			}
		}
	}
}
