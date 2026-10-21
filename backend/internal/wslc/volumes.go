package wslc

import (
	"context"
	"strings"
	"time"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
)

// VolumesList implements wslc/volumes/list.
func (s *Service) VolumesList(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 60*time.Second, "volume", "list", "--format", "json")
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("列出数据卷失败", result.Stdout, result.Stderr)
	}
	volumes, warnings := parse.Volumes(result.Stdout)
	return map[string]any{"volumes": volumes, "warnings": warnings}, nil
}

// VolumeInspectParams is the request of wslc/volumes/inspect.
type VolumeInspectParams struct {
	Name string `json:"name"`
}

// VolumesInspect implements wslc/volumes/inspect.
func (s *Service) VolumesInspect(ctx context.Context, name string) (any, *apperr.Error) {
	volume, appErr := requireText("name", name)
	if appErr != nil {
		return nil, appErr
	}
	result, appErr := s.run(ctx, 60*time.Second, "volume", "inspect", "--format", "json", volume)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, inspectFailure("检查数据卷失败", result)
	}
	value, err := parse.FirstJSONValue([]byte(result.Stdout))
	if err != nil {
		return nil, apperr.Internal("解析数据卷详情失败", result.Stdout)
	}
	return map[string]any{"raw": value}, nil
}

// CreateVolumeParams is the request of wslc/volumes/create.
type CreateVolumeParams struct {
	Name string `json:"name"`
}

// VolumesCreate implements wslc/volumes/create.
func (s *Service) VolumesCreate(ctx context.Context, params CreateVolumeParams) (any, *apperr.Error) {
	name := strings.TrimSpace(params.Name)
	args := []string{"volume", "create"}
	if name != "" {
		if len(name) > 256 || strings.ContainsAny(name, "\r\n\t\x00") {
			return nil, apperr.InvalidParams("name 不合法", "数据卷名称不能包含控制字符", "")
		}
		args = append(args, name)
	}
	result, appErr := s.run(ctx, 60*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("创建数据卷失败", result.Stdout, result.Stderr)
	}
	created := outputText(result)
	if created == "" {
		created = name
	}
	if created == "" {
		return nil, apperr.Internal("无法解析数据卷名称", result.Stdout+result.Stderr)
	}
	return map[string]any{"name": created}, nil
}

// RemoveVolumesParams is the request of wslc/volumes/remove.
type RemoveVolumesParams struct {
	Names []string `json:"names"`
}

// VolumesRemove implements wslc/volumes/remove.
func (s *Service) VolumesRemove(ctx context.Context, params RemoveVolumesParams) (any, *apperr.Error) {
	names, appErr := requireList("names", params.Names)
	if appErr != nil {
		return nil, appErr
	}
	args := append([]string{"volume", "remove"}, names...)
	result, appErr := s.run(ctx, 120*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("移除数据卷失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"removed": names, "output": outputText(result)}, nil
}

// VolumesPrune implements wslc/volumes/prune.
func (s *Service) VolumesPrune(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 120*time.Second, "volume", "prune", "--force")
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("清理数据卷失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"output": outputText(result)}, nil
}
