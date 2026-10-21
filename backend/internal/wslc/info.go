package wslc

import (
	"context"
	"strings"
	"time"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
)

// Info implements wslc/info.
func (s *Service) Info(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 60*time.Second, "info", "--format", "json")
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("读取 wslc 信息失败", result.Stdout, result.Stderr)
	}
	info, err := parse.ParseInfo([]byte(result.Stdout))
	if err != nil {
		return nil, apperr.Internal("解析 wslc info 输出失败", result.Stdout)
	}
	if info.Version == "" {
		if versionResult, versionErr := s.run(ctx, 30*time.Second, "version"); versionErr == nil {
			info.Version = parse.ParseVersionText(versionResult.Stdout)
			if info.Client != nil && info.Client.Version == "" {
				info.Client.Version = info.Version
			}
		}
	}
	if info.Server != nil && info.Server.Sessions == nil {
		info.Server.Sessions = []parse.ServerSession{}
	}
	return info, nil
}

// ClientVersion returns the wslc client version (used by system/info fallback).
func (s *Service) ClientVersion(ctx context.Context) string {
	result, appErr := s.run(ctx, 30*time.Second, "version")
	if appErr != nil {
		return ""
	}
	return strings.TrimSpace(parse.ParseVersionText(result.Stdout))
}
