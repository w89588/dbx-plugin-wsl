package wsl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
	"github.com/w89588/dbx-plugin-wsl/internal/winenv"
)

const maxConfigBytes = 256 * 1024

// Serialize configuration writes, including read/modify/write idle-policy updates.
var configMu sync.Mutex

// SystemInfo implements system/info. It never fails just because a tool is
// missing; missing tools are reported through available=false and warnings.
func (s *Service) SystemInfo(parent context.Context) (any, *apperr.Error) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	warnings := make([]string, 0, 4)
	platform := map[string]any{
		"os":             "windows",
		"arch":           runtime.GOARCH,
		"hostname":       winenv.Hostname(),
		"username":       winenv.Username(),
		"windowsVersion": winenv.WindowsVersion(),
	}

	wslBlock := map[string]any{
		"available":    false,
		"exe":          "",
		"version":      versionMap(parse.Version{}),
		"status":       statusMap(parse.Status{}),
		"distroCount":  0,
		"runningCount": 0,
	}
	if executable, err := s.Ex.Resolve(execx.ToolWSL); err != nil {
		warnings = append(warnings, "wsl.exe 未找到，发行版管理不可用")
	} else {
		wslBlock["available"] = true
		wslBlock["exe"] = executable
		if result, appErr := s.run(ctx, 20*time.Second, "--version"); appErr == nil {
			version := parse.ParseVersion(result.Stdout)
			wslBlock["version"] = versionMap(version)
			if version.Windows != "" {
				platform["windowsVersion"] = version.Windows
			}
		} else {
			warnings = append(warnings, "读取 wsl 版本失败: "+appErr.Message)
		}
		if result, appErr := s.run(ctx, 20*time.Second, "--status"); appErr == nil {
			wslBlock["status"] = statusMap(parse.ParseStatus(result.Stdout))
		} else {
			warnings = append(warnings, "读取 wsl 状态失败: "+appErr.Message)
		}
		if result, appErr := s.run(ctx, 20*time.Second, "--list", "--verbose"); appErr == nil {
			entries := parse.ParseDistroList(result.Stdout)
			running := 0
			for _, entry := range entries {
				if entry.State == "Running" {
					running++
				}
			}
			wslBlock["distroCount"] = len(entries)
			wslBlock["runningCount"] = running
		} else {
			warnings = append(warnings, "列出发行版失败: "+appErr.Message)
		}
	}

	wslcBlock := map[string]any{
		"available": false,
		"exe":       "",
		"version":   "",
		"client":    nil,
		"server":    nil,
	}
	if executable, err := s.Ex.Resolve(execx.ToolWSLc); err != nil {
		warnings = append(warnings, "wslc.exe 未找到，容器管理不可用")
	} else {
		wslcBlock["available"] = true
		wslcBlock["exe"] = executable
		if result, appErr := s.runTool(ctx, execx.ToolWSLc, 30*time.Second, "info", "--format", "json"); appErr == nil && strings.TrimSpace(result.Stdout) != "" {
			if info, err := parse.ParseInfo([]byte(result.Stdout)); err == nil {
				wslcBlock["version"] = info.Version
				wslcBlock["client"] = info.Client
				wslcBlock["server"] = info.Server
			} else {
				warnings = append(warnings, "解析 wslc info 输出失败")
			}
		} else {
			warnings = append(warnings, "读取 wslc 信息失败")
		}
	}

	return map[string]any{
		"platform": platform,
		"wsl":      wslBlock,
		"wslc":     wslcBlock,
		"warnings": warnings,
	}, nil
}

func versionMap(version parse.Version) map[string]string {
	return map[string]string{
		"wsl":      version.WSL,
		"kernel":   version.Kernel,
		"wslg":     version.WSLg,
		"msrdc":    version.MSRDC,
		"direct3d": version.Direct3D,
		"dxcore":   version.DXCore,
		"windows":  version.Windows,
	}
}

func statusMap(status parse.Status) map[string]any {
	return map[string]any{
		"defaultDistro":  status.DefaultDistro,
		"defaultVersion": status.DefaultVersion,
		"wsl1Supported":  status.WSL1Supported,
		"raw":            status.Raw,
	}
}

// WSLStatus implements system/wsl/status.
func (s *Service) WSLStatus(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 30*time.Second, "--status")
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("读取状态", result); failure != nil {
		return nil, failure
	}
	status := parse.ParseStatus(result.Stdout)
	return map[string]any{
		"defaultDistro":  status.DefaultDistro,
		"defaultVersion": status.DefaultVersion,
		"wsl1Supported":  status.WSL1Supported,
		"raw":            status.Raw,
	}, nil
}

// ConfigRead implements system/config/read.
func (s *Service) ConfigRead(ctx context.Context) (any, *apperr.Error) {
	wslconfig, appErr := readConfigFile(filepath.Join(winenv.HomeDir(), ".wslconfig"))
	if appErr != nil {
		return nil, appErr
	}
	wslcPath := s.WslcSettingsPath(ctx)
	wslcSettings, appErr := readConfigFile(wslcPath)
	if appErr != nil {
		return nil, appErr
	}
	return map[string]any{
		"wslconfig":    wslconfig,
		"wslcSettings": wslcSettings,
	}, nil
}

func readConfigFile(path string) (map[string]any, *apperr.Error) {
	payload := map[string]any{"path": path, "exists": false, "content": ""}
	if strings.TrimSpace(path) == "" {
		return payload, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return payload, nil
		}
		return nil, apperr.Internal("读取配置文件失败", err.Error()+"\n"+path)
	}
	payload["exists"] = true
	payload["content"] = execx.DecodeText(raw)
	return payload, nil
}

// WslcSettingsPath returns the wslc settings file path. It asks wslc for the
// path so the value stays correct when the user moves it.
func (s *Service) WslcSettingsPath(ctx context.Context) string {
	if result, appErr := s.runTool(ctx, execx.ToolWSLc, 20*time.Second, "info", "--format", "json"); appErr == nil && result.ExitCode == 0 {
		if info, err := parse.ParseInfo([]byte(result.Stdout)); err == nil && info.Client != nil && info.Client.SettingsFile != "" {
			return info.Client.SettingsFile
		}
	}
	return filepath.Join(winenv.LocalAppData(), "wslc", "settings.yaml")
}

// ConfigWriteParams is the request of system/config/write.
type ConfigWriteParams struct {
	Target  string `json:"target"`
	Content string `json:"content"`
}

// ConfigWrite implements system/config/write with backup + atomic replace.
func (s *Service) ConfigWrite(ctx context.Context, params ConfigWriteParams) (any, *apperr.Error) {
	configMu.Lock()
	defer configMu.Unlock()
	target := strings.ToLower(strings.TrimSpace(params.Target))
	var path string
	switch target {
	case "wslconfig":
		path = filepath.Join(winenv.HomeDir(), ".wslconfig")
	case "wslc", "wslcsettings":
		path = s.WslcSettingsPath(ctx)
	default:
		return nil, apperr.InvalidParams("target 必须是 wslconfig 或 wslc", "请检查请求参数", params.Target)
	}
	if path == "" {
		return nil, apperr.InvalidParams("无法解析配置文件路径", "请检查用户目录环境变量", "")
	}
	return writeConfigFile(path, params.Content)
}

func writeConfigFile(path, text string) (map[string]any, *apperr.Error) {
	content := []byte(text)
	if len(content) > maxConfigBytes {
		return nil, apperr.InvalidParams("配置内容超过 256 KiB 上限", "请精简配置后重试", "")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, apperr.Internal("创建配置目录失败", err.Error())
	}

	var backupPath any
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		name := path + ".bak-" + time.Now().Format("20060102-150405")
		for suffix := 1; ; suffix++ {
			if _, statErr := os.Stat(name); errors.Is(statErr, os.ErrNotExist) {
				break
			}
			name = path + ".bak-" + time.Now().Format("20060102-150405") + "-" + strconv.Itoa(suffix)
		}
		original, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, apperr.Internal("备份配置文件失败", readErr.Error())
		}
		if writeErr := os.WriteFile(name, original, 0o644); writeErr != nil {
			return nil, apperr.Internal("写入备份文件失败", writeErr.Error())
		}
		backupPath = name
	}

	temporary, err := os.CreateTemp(directory, ".dbx-wsl-config-*.tmp")
	if err != nil {
		return nil, apperr.Internal("创建临时文件失败", err.Error())
	}
	temporaryName := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryName) }
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		cleanup()
		return nil, apperr.Internal("写入临时文件失败", err.Error())
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return nil, apperr.Internal("关闭临时文件失败", err.Error())
	}
	if err := os.Rename(temporaryName, path); err != nil {
		cleanup()
		return nil, apperr.Internal("替换配置文件失败", err.Error())
	}
	return map[string]any{
		"path":       path,
		"backupPath": backupPath,
		"bytes":      len(content),
	}, nil
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

// Online implements system/wsl/online. Network failures return an empty list
// plus an error string instead of an RPC error (contract 4.1).
func (s *Service) Online(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.runTool(ctx, execx.ToolWSL, 10*time.Second, "--list", "--online")
	if appErr != nil {
		if appErr.Code == apperr.CodeToolNotFound {
			return nil, appErr
		}
		return map[string]any{"distros": []any{}, "error": appErr.Message}, nil
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		return map[string]any{"distros": []any{}, "error": detail}, nil
	}
	entries := parse.ParseOnlineList(result.Stdout)
	distros := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		distros = append(distros, map[string]string{
			"name":         entry.Name,
			"friendlyName": entry.FriendlyName,
		})
	}
	return map[string]any{"distros": distros, "error": nil}, nil
}

// ShutdownParams is the request of wsl/shutdown.
type ShutdownParams struct {
	Force bool `json:"force"`
}

// Shutdown implements wsl/shutdown.
func (s *Service) Shutdown(ctx context.Context, params ShutdownParams) (any, *apperr.Error) {
	args := []string{"--shutdown"}
	if params.Force {
		args = append(args, "--force")
	}
	result, appErr := s.run(ctx, 120*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("关闭 WSL", result); failure != nil {
		return nil, failure
	}
	return map[string]any{"shutdown": true}, nil
}

// UpdateParams is the request of wsl/update.
type UpdateParams struct {
	PreRelease bool `json:"preRelease"`
}

// Update implements wsl/update as a long running job.
func (s *Service) Update(ctx context.Context, emitter *dbxpluginsdk.Emitter, params UpdateParams) (any, *apperr.Error) {
	args := []string{"--update"}
	if params.PreRelease {
		args = append(args, "--pre-release")
	}
	return s.startJob("wsl/update", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		result, appErr := s.streamJob(jobCtx, job, execx.Request{Tool: execx.ToolWSL, Args: args}, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"output": strings.TrimSpace(result.Stdout)}, nil
	})
}
