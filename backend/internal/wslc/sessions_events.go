package wslc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
)

// SessionsList implements wslc/sessions/list.
func (s *Service) SessionsList(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 60*time.Second, "system", "session", "list")
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("列出会话失败", result.Stdout, result.Stderr)
	}
	entries := parse.ParseSessions(result.Stdout)
	sessions := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		sessions = append(sessions, map[string]any{
			"id":          entry.ID,
			"creatorPid":  entry.CreatorPid,
			"displayName": entry.DisplayName,
		})
	}
	return map[string]any{"sessions": sessions}, nil
}

// EventsParams is the request of wslc/events/start.
type EventsParams struct {
	Since   string   `json:"since"`
	Until   string   `json:"until"`
	Filters []string `json:"filters"`
}

// EventsStart implements wslc/events/start: it subscribes to the wslc events
// stream inside a job and forwards each event as a `wslc/event` event.
func (s *Service) EventsStart(ctx context.Context, emitter *dbxpluginsdk.Emitter, params EventsParams) (any, *apperr.Error) {
	args := []string{"events", "--format", "json"}
	if since := strings.TrimSpace(params.Since); since != "" {
		args = append(args, "--since", since)
	}
	if until := strings.TrimSpace(params.Until); until != "" {
		args = append(args, "--until", until)
	}
	for _, filter := range params.Filters {
		trimmed := strings.TrimSpace(filter)
		if trimmed == "" {
			continue
		}
		args = append(args, "--filter", trimmed)
	}
	return s.startJob("wslc/events/start", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJob(jobCtx, job, args, func(stream, text string) {
			line := strings.TrimSpace(text)
			if line == "" || !json.Valid([]byte(line)) {
				return
			}
			job.Event("wslc/event", map[string]any{
				"jobId": job.ID(),
				"event": json.RawMessage(line),
			})
		})
		if appErr != nil {
			return nil, appErr
		}
		return nil, nil
	})
}
