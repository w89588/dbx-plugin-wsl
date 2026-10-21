package wslc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
	"github.com/w89588/dbx-plugin-wsl/internal/winenv"
)

// ImagesList implements wslc/images/list.
func (s *Service) ImagesList(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 60*time.Second, "images", "--format", "json")
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("列出镜像失败", result.Stdout, result.Stderr)
	}
	images, warnings := parse.Images(result.Stdout)
	return map[string]any{"images": images, "warnings": warnings}, nil
}

// ImageInspectParams is the request of wslc/images/inspect.
type ImageInspectParams struct {
	Ref string `json:"ref"`
}

// ImagesInspect implements wslc/images/inspect.
func (s *Service) ImagesInspect(ctx context.Context, ref string) (any, *apperr.Error) {
	reference, appErr := requireText("ref", ref)
	if appErr != nil {
		return nil, appErr
	}
	result, appErr := s.run(ctx, 60*time.Second, "inspect", "--type", "image", "--format", "json", reference)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, inspectFailure("检查镜像失败", result)
	}
	value, err := parse.FirstJSONValue([]byte(result.Stdout))
	if err != nil {
		return nil, apperr.Internal("解析镜像详情失败", result.Stdout)
	}
	return map[string]any{"raw": value}, nil
}

// PullParams is the request of wslc/images/pull.
type PullParams struct {
	Ref     string `json:"ref"`
	AllTags bool   `json:"allTags"`
}

// ImagesPull implements wslc/images/pull as a long running job.
func (s *Service) ImagesPull(ctx context.Context, emitter *dbxpluginsdk.Emitter, params PullParams) (any, *apperr.Error) {
	reference, appErr := requireText("ref", params.Ref)
	if appErr != nil {
		return nil, appErr
	}
	args := []string{"pull", reference}
	if params.AllTags {
		args = append(args, "--all-tags")
	}
	return s.startJob("wslc/images/pull", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJobWith(jobCtx, job, args, func(stream, text string) {
			job.Line(stream, text)
		}, classifyRegistryFailure)
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"ref": reference}, nil
	})
}

// RemoveImagesParams is the request of wslc/images/remove.
type RemoveImagesParams struct {
	Refs  []string `json:"refs"`
	Force bool     `json:"force"`
}

// ImagesRemove implements wslc/images/remove.
func (s *Service) ImagesRemove(ctx context.Context, params RemoveImagesParams) (any, *apperr.Error) {
	references, appErr := requireList("refs", params.Refs)
	if appErr != nil {
		return nil, appErr
	}
	args := []string{"image", "remove"}
	if params.Force {
		args = append(args, "--force")
	}
	args = append(args, references...)
	result, appErr := s.run(ctx, 180*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("移除镜像失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"removed": references, "output": outputText(result)}, nil
}

// PruneImagesParams is the request of wslc/images/prune.
type PruneImagesParams struct {
	All bool `json:"all"`
}

// ImagesPrune implements wslc/images/prune as a long running job.
func (s *Service) ImagesPrune(ctx context.Context, emitter *dbxpluginsdk.Emitter, params PruneImagesParams) (any, *apperr.Error) {
	args := []string{"image", "prune", "--force"}
	if params.All {
		args = append(args, "--all")
	}
	return s.startJob("wslc/images/prune", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		result, appErr := s.streamJob(jobCtx, job, args, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"output": strings.TrimSpace(result.Stdout)}, nil
	})
}

// TagParams is the request of wslc/images/tag.
type TagParams struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// ImagesTag implements wslc/images/tag.
func (s *Service) ImagesTag(ctx context.Context, params TagParams) (any, *apperr.Error) {
	source, appErr := requireText("source", params.Source)
	if appErr != nil {
		return nil, appErr
	}
	target, appErr := requireText("target", params.Target)
	if appErr != nil {
		return nil, appErr
	}
	result, appErr := s.run(ctx, 60*time.Second, "tag", source, target)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("标记镜像失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"source": source, "target": target}, nil
}

// BuildParams is the request of wslc/images/build.
type BuildParams struct {
	Path       string            `json:"path"`
	Tag        string            `json:"tag"`
	Dockerfile string            `json:"dockerfile"`
	BuildArgs  map[string]string `json:"buildArgs"`
	NoCache    bool              `json:"noCache"`
	Target     string            `json:"target"`
}

// ImagesBuild implements wslc/images/build as a long running job.
func (s *Service) ImagesBuild(ctx context.Context, emitter *dbxpluginsdk.Emitter, params BuildParams) (any, *apperr.Error) {
	tag, appErr := requireText("tag", params.Tag)
	if appErr != nil {
		return nil, appErr
	}
	contextPath, appErr := requireText("path", params.Path)
	if appErr != nil {
		return nil, appErr
	}
	contextPath = winenv.ExpandPath(contextPath)
	absolute, err := filepath.Abs(contextPath)
	if err != nil {
		return nil, apperr.InvalidParams("path 不合法", "请提供绝对路径", err.Error())
	}
	if info, statErr := os.Stat(absolute); statErr != nil || !info.IsDir() {
		return nil, apperr.NotFound("构建上下文目录不存在: "+absolute, "请检查 path", "")
	}
	dockerfile := strings.TrimSpace(params.Dockerfile)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	if !filepath.IsAbs(dockerfile) {
		dockerfile = filepath.Join(absolute, dockerfile)
	}
	args := []string{"build", "--tag", tag, "--file", dockerfile}
	for _, pair := range sortedEnv(params.BuildArgs) {
		args = append(args, "--build-arg", pair)
	}
	if params.NoCache {
		args = append(args, "--no-cache")
	}
	if target := strings.TrimSpace(params.Target); target != "" {
		args = append(args, "--target", target)
	}
	args = append(args, absolute)
	return s.startJob("wslc/images/build", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJobWith(jobCtx, job, args, func(stream, text string) {
			job.Line(stream, text)
		}, classifyRegistryFailure)
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"tag": tag, "path": absolute}, nil
	})
}

// SaveParams is the request of wslc/images/save.
type SaveParams struct {
	Refs []string `json:"refs"`
	Path string   `json:"path"`
}

// ImagesSave implements wslc/images/save as a long running job.
func (s *Service) ImagesSave(ctx context.Context, emitter *dbxpluginsdk.Emitter, params SaveParams) (any, *apperr.Error) {
	references, appErr := requireList("refs", params.Refs)
	if appErr != nil {
		return nil, appErr
	}
	target, appErr := outputPath(params.Path)
	if appErr != nil {
		return nil, appErr
	}
	args := append([]string{"save", "--output", target}, references...)
	return s.startJob("wslc/images/save", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJob(jobCtx, job, args, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"path": target, "refs": references}, nil
	})
}

// LoadParams is the request of wslc/images/load.
type LoadParams struct {
	Path string `json:"path"`
}

// ImagesLoad implements wslc/images/load as a long running job.
func (s *Service) ImagesLoad(ctx context.Context, emitter *dbxpluginsdk.Emitter, params LoadParams) (any, *apperr.Error) {
	source, appErr := existingFilePath(params.Path)
	if appErr != nil {
		return nil, appErr
	}
	args := []string{"load", "--input", source}
	return s.startJob("wslc/images/load", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJob(jobCtx, job, args, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"path": source}, nil
	})
}

// outputPath expands a user supplied output path and creates its parent.
func outputPath(value string) (string, *apperr.Error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", apperr.InvalidParams("path 不能为空", "请提供文件路径", "")
	}
	absolute, err := filepath.Abs(winenv.ExpandPath(trimmed))
	if err != nil {
		return "", apperr.InvalidParams("path 不合法", "请提供绝对路径", err.Error())
	}
	absolute = filepath.Clean(absolute)
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return "", apperr.Internal("创建目标目录失败", err.Error())
	}
	return absolute, nil
}

// existingFilePath expands a path and verifies that it points at a file.
func existingFilePath(value string) (string, *apperr.Error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", apperr.InvalidParams("path 不能为空", "请提供文件路径", "")
	}
	absolute, err := filepath.Abs(winenv.ExpandPath(trimmed))
	if err != nil {
		return "", apperr.InvalidParams("path 不合法", "请提供绝对路径", err.Error())
	}
	absolute = filepath.Clean(absolute)
	info, statErr := os.Stat(absolute)
	if statErr != nil || info.IsDir() {
		return "", apperr.NotFound("文件不存在: "+absolute, "请检查文件路径", "")
	}
	return absolute, nil
}
