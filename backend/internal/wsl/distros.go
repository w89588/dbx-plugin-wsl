package wsl

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
	"github.com/w89588/dbx-plugin-wsl/internal/winenv"
)

// distroSnapshot is the merged `wsl -l -v` + registry view.
type distroSnapshot struct {
	Entries     []parse.DistroEntry
	Registry    map[string]winenv.LxssDistro
	DefaultName string
	DefaultGUID string
}

type distroOut struct {
	Name       string `json:"name"`
	GUID       string `json:"guid"`
	Version    int    `json:"version"`
	State      string `json:"state"`
	Running    bool   `json:"running"`
	IsDefault  bool   `json:"isDefault"`
	BasePath   string `json:"basePath"`
	VhdPath    string `json:"vhdPath"`
	VhdBytes   int64  `json:"vhdBytes"`
	Sparse     any    `json:"sparse"`
	DefaultUid int    `json:"defaultUid"`
}

// DistroList implements wsl/distros/list.
func (s *Service) DistroList(ctx context.Context) (any, *apperr.Error) {
	snapshot, appErr := s.snapshot(ctx)
	if appErr != nil {
		return nil, appErr
	}
	distros := make([]distroOut, 0, len(snapshot.Entries))
	defaultVersion := 0
	for _, entry := range snapshot.Entries {
		registry := snapshot.Registry[strings.ToLower(entry.Name)]
		version := entry.Version
		if version == 0 {
			version = registry.Version
		}
		isDefault := entry.IsDefault
		if !isDefault && snapshot.DefaultName != "" && strings.EqualFold(snapshot.DefaultName, entry.Name) {
			isDefault = true
		}
		if isDefault && version != 0 {
			defaultVersion = version
		}
		vhdPath := ""
		var vhdBytes int64
		if registry.BasePath != "" {
			vhdPath = filepath.Join(registry.BasePath, "ext4.vhdx")
			if info, err := os.Stat(vhdPath); err == nil && !info.IsDir() {
				vhdBytes = info.Size()
			}
		}
		distros = append(distros, distroOut{
			Name:       entry.Name,
			GUID:       registry.GUID,
			Version:    version,
			State:      entry.State,
			Running:    entry.State == "Running",
			IsDefault:  isDefault,
			BasePath:   registry.BasePath,
			VhdPath:    vhdPath,
			VhdBytes:   vhdBytes,
			Sparse:     nil,
			DefaultUid: registry.DefaultUid,
		})
	}
	return map[string]any{
		"distros":        distros,
		"defaultDistro":  snapshot.DefaultName,
		"defaultVersion": defaultVersion,
		"count":          len(distros),
	}, nil
}

// snapshot reads `wsl --list --verbose` and merges the registry metadata.
func (s *Service) snapshot(ctx context.Context) (distroSnapshot, *apperr.Error) {
	snapshot := distroSnapshot{Registry: map[string]winenv.LxssDistro{}}
	result, appErr := s.run(ctx, 60*time.Second, "--list", "--verbose")
	if appErr != nil {
		return snapshot, appErr
	}
	entries := parse.ParseDistroList(result.Stdout)
	if result.ExitCode != 0 && len(entries) == 0 {
		combined := result.Stdout + "\n" + result.Stderr
		if apperr.ContainsAny(combined, "没有已安装", "no installed distributions", "尚未安装") {
			// Fresh machine without distributions: valid empty state.
			return snapshot, nil
		}
		if failure := s.launchFailure("列出发行版", result); failure != nil {
			return snapshot, failure
		}
	}
	snapshot.Entries = entries
	registry, err := winenv.ReadLxss()
	if err == nil {
		snapshot.DefaultGUID = registry.DefaultDistributionGUID
		for _, distro := range registry.Distros {
			snapshot.Registry[strings.ToLower(distro.DistributionName)] = distro
			if snapshot.DefaultGUID != "" && strings.EqualFold(distro.GUID, snapshot.DefaultGUID) {
				snapshot.DefaultName = distro.DistributionName
			}
		}
	}
	for _, entry := range entries {
		if entry.IsDefault && snapshot.DefaultName == "" {
			snapshot.DefaultName = entry.Name
		}
	}
	return snapshot, nil
}

// lookup returns one mounted distribution from the snapshot.
func (s *Service) lookup(ctx context.Context, name string) (parse.DistroEntry, distroSnapshot, bool, *apperr.Error) {
	snapshot, appErr := s.snapshot(ctx)
	if appErr != nil {
		return parse.DistroEntry{}, snapshot, false, appErr
	}
	for _, entry := range snapshot.Entries {
		if strings.EqualFold(entry.Name, name) {
			return entry, snapshot, true, nil
		}
	}
	return parse.DistroEntry{}, snapshot, false, nil
}

// DistroStart implements wsl/distros/start.
func (s *Service) DistroStart(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	entry, _, found, appErr := s.lookup(ctx, name)
	if appErr != nil {
		return nil, appErr
	}
	if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	wasRunning := entry.State == "Running"
	result, appErr := s.run(ctx, 120*time.Second, "--distribution", name, "--exec", "true")
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("启动发行版", result); failure != nil {
		return nil, failure
	}
	return map[string]any{"name": name, "state": "Running", "changed": !wasRunning}, nil
}

// DistroTerminate implements wsl/distros/terminate.
func (s *Service) DistroTerminate(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	entry, _, found, appErr := s.lookup(ctx, name)
	if appErr != nil {
		return nil, appErr
	}
	if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	result, appErr := s.run(ctx, 60*time.Second, "--terminate", name)
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("终止发行版", result); failure != nil {
		return nil, failure
	}
	return map[string]any{"name": name, "state": "Stopped", "changed": entry.State == "Running"}, nil
}

// DistroSetDefault implements wsl/distros/setDefault.
func (s *Service) DistroSetDefault(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	result, appErr := s.run(ctx, 30*time.Second, "--set-default", name)
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("设置默认发行版", result); failure != nil {
		return nil, failure
	}
	return map[string]any{"defaultDistro": name}, nil
}

// ExecParams is the request of wsl/distros/exec.
type ExecParams struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	User      string `json:"user"`
	TimeoutMs int    `json:"timeoutMs"`
}

// DistroExec implements wsl/distros/exec.
func (s *Service) DistroExec(ctx context.Context, params ExecParams) (any, *apperr.Error) {
	name, appErr := validateName(params.Name)
	if appErr != nil {
		return nil, appErr
	}
	command := strings.TrimSpace(params.Command)
	if command == "" {
		return nil, apperr.InvalidParams("command 不能为空", "请提供要执行的命令", "")
	}
	timeout := 60 * time.Second
	if params.TimeoutMs > 0 {
		timeout = time.Duration(params.TimeoutMs) * time.Millisecond
		if timeout > 300*time.Second {
			timeout = 300 * time.Second
		}
	}
	args := []string{"--distribution", name}
	if cwd := strings.TrimSpace(params.Cwd); cwd != "" {
		args = append(args, "--cd", cwd)
	}
	if user := strings.TrimSpace(params.User); user != "" {
		args = append(args, "--user", user)
	}
	args = append(args, "--exec", "sh", "-lc", command)
	result, appErr := s.run(ctx, timeout, args...)
	if appErr != nil {
		return nil, appErr
	}
	if isWSLError(result) {
		return nil, s.launchFailure("执行命令", result)
	}
	return map[string]any{
		"name":       name,
		"exitCode":   result.ExitCode,
		"stdout":     result.Stdout,
		"stderr":     result.Stderr,
		"durationMs": result.DurationMs,
		"truncated":  result.Truncated,
		"command":    result.Command,
		"started":    true,
	}, nil
}

const distroInfoScript = `printf '@@OS_RELEASE@@\n'
cat /etc/os-release 2>/dev/null
printf '@@KERNEL@@\n'
uname -r 2>/dev/null
printf '@@ARCH@@\n'
uname -m 2>/dev/null
printf '@@UID@@\n'
id -u 2>/dev/null
printf '@@USER@@\n'
id -un 2>/dev/null
printf '@@SHELL@@\n'
getent passwd "$(id -u)" 2>/dev/null | cut -d: -f7
printf '@@DISK@@\n'
df -B1 / 2>/dev/null | tail -n 1
printf '@@IPV4@@\n'
hostname -I 2>/dev/null
printf '@@UPTIME@@\n'
uptime -p 2>/dev/null || uptime 2>/dev/null
printf '@@WSLCONF@@\n'
cat /etc/wsl.conf 2>/dev/null
printf '@@END@@\n'
`

var distroInfoMarkers = []string{
	"@@OS_RELEASE@@", "@@KERNEL@@", "@@ARCH@@", "@@UID@@", "@@USER@@",
	"@@SHELL@@", "@@DISK@@", "@@IPV4@@", "@@UPTIME@@", "@@WSLCONF@@", "@@END@@",
}

// DistroInfo implements wsl/distros/info. It starts the distribution on demand.
func (s *Service) DistroInfo(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	result, appErr := s.run(ctx, 120*time.Second, "--distribution", name, "--exec", "sh", "-lc", distroInfoScript)
	if appErr != nil {
		return nil, appErr
	}
	if isWSLError(result) {
		return nil, s.launchFailure("读取发行版信息", result)
	}
	sections := parse.ParseSections(result.Stdout, distroInfoMarkers)
	osRelease := parse.ParseKeyValueBlock(sections["@@OS_RELEASE@@"])
	if osRelease == nil {
		osRelease = map[string]string{}
	}
	uid := 0
	if parsed, err := strconv.Atoi(strings.TrimSpace(sections["@@UID@@"])); err == nil {
		uid = parsed
	}
	ipv4 := make([]string, 0, 2)
	for _, field := range strings.Fields(sections["@@IPV4@@"]) {
		if ip := net.ParseIP(field); ip != nil && ip.To4() != nil {
			ipv4 = append(ipv4, field)
		}
	}
	return map[string]any{
		"name":         name,
		"running":      true,
		"osRelease":    osRelease,
		"kernel":       strings.TrimSpace(sections["@@KERNEL@@"]),
		"arch":         strings.TrimSpace(sections["@@ARCH@@"]),
		"uid":          uid,
		"userName":     strings.TrimSpace(sections["@@USER@@"]),
		"defaultShell": strings.TrimSpace(sections["@@SHELL@@"]),
		"disk":         parseDiskUsage(sections["@@DISK@@"]),
		"ipv4":         ipv4,
		"uptime":       strings.TrimSpace(sections["@@UPTIME@@"]),
		"wslConf":      sections["@@WSLCONF@@"],
	}, nil
}

func parseDiskUsage(text string) map[string]any {
	disk := map[string]any{
		"totalBytes": int64(0),
		"usedBytes":  int64(0),
		"availBytes": int64(0),
		"usePercent": 0,
	}
	fields := strings.Fields(text)
	if len(fields) >= 4 {
		if total, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			disk["totalBytes"] = total
		}
		if used, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
			disk["usedBytes"] = used
		}
		if avail, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
			disk["availBytes"] = avail
		}
	}
	if len(fields) >= 5 {
		if percent, err := strconv.Atoi(strings.TrimSuffix(fields[4], "%")); err == nil {
			disk["usePercent"] = percent
		}
	}
	return disk
}

const wslConfScript = `if [ -f /etc/wsl.conf ]; then printf '@@EXISTS@@\n'; cat /etc/wsl.conf; fi`

// DistroWslConf implements wsl/distros/wslconf.
func (s *Service) DistroWslConf(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	result, appErr := s.run(ctx, 60*time.Second, "--distribution", name, "--exec", "sh", "-lc", wslConfScript)
	if appErr != nil {
		return nil, appErr
	}
	if isWSLError(result) {
		return nil, s.launchFailure("读取 wsl.conf", result)
	}
	const marker = "@@EXISTS@@"
	exists := false
	content := ""
	if index := strings.Index(result.Stdout, marker); index >= 0 {
		exists = true
		content = strings.TrimPrefix(result.Stdout[index+len(marker):], "\r\n")
		content = strings.TrimPrefix(content, "\n")
	}
	return map[string]any{
		"path":    "/etc/wsl.conf",
		"exists":  exists,
		"content": content,
	}, nil
}

// ExportParams is the request of wsl/distros/export.
type ExportParams struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Format string `json:"format"`
}

var exportFormats = map[string]struct{}{
	"tar": {}, "tar.gz": {}, "tar.xz": {}, "vhd": {},
}

// DistroExport implements wsl/distros/export as a long running job.
func (s *Service) DistroExport(ctx context.Context, emitter *dbxpluginsdk.Emitter, params ExportParams) (any, *apperr.Error) {
	name, appErr := validateName(params.Name)
	if appErr != nil {
		return nil, appErr
	}
	format := strings.ToLower(strings.TrimSpace(params.Format))
	if format == "" {
		format = "tar"
	}
	if _, ok := exportFormats[format]; !ok {
		return nil, apperr.InvalidParams("format 必须是 tar / tar.gz / tar.xz / vhd", "请检查导出格式", params.Format)
	}
	target, appErr := exportTarget(params.Path)
	if appErr != nil {
		return nil, appErr
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	args := []string{"--export", name, target, "--format", format}
	return s.startJob("wsl/distros/export", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJob(jobCtx, job, execx.Request{Tool: execx.ToolWSL, Args: args}, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		var bytes int64
		if info, err := os.Stat(target); err == nil {
			bytes = info.Size()
		}
		return map[string]any{"path": target, "bytes": bytes}, nil
	})
}

func exportTarget(value string) (string, *apperr.Error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", apperr.InvalidParams("path 不能为空", "请提供导出文件路径", "")
	}
	expanded := winenv.ExpandPath(trimmed)
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", apperr.InvalidParams("path 不合法", "请提供绝对路径", err.Error())
	}
	absolute = filepath.Clean(absolute)
	if _, err := os.Stat(absolute); err == nil {
		return "", apperr.Conflict("目标文件已存在: "+absolute, "请更换导出路径或先删除已有文件", "")
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return "", apperr.Internal("创建导出目录失败", err.Error())
	}
	return absolute, nil
}

// ImportParams is the request of wsl/distros/import.
type ImportParams struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Path     string `json:"path"`
	Version  int    `json:"version"`
	Vhd      bool   `json:"vhd"`
}

// DistroImport implements wsl/distros/import as a long running job.
func (s *Service) DistroImport(ctx context.Context, emitter *dbxpluginsdk.Emitter, params ImportParams) (any, *apperr.Error) {
	name, appErr := validateName(params.Name)
	if appErr != nil {
		return nil, appErr
	}
	if params.Version != 0 && params.Version != 1 && params.Version != 2 {
		return nil, apperr.InvalidParams("version 必须是 1 或 2", "请检查请求参数", "")
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if found {
		return nil, apperr.Conflict("发行版已存在: "+name, "请更换名称", "")
	}
	location := strings.TrimSpace(params.Location)
	if location == "" {
		return nil, apperr.InvalidParams("location 不能为空", "请提供安装位置", "")
	}
	location, appErr = absolutePath(location)
	if appErr != nil {
		return nil, appErr
	}
	source := strings.TrimSpace(params.Path)
	if source == "" {
		return nil, apperr.InvalidParams("path 不能为空", "请提供要导入的 tar/vhd 文件", "")
	}
	source, appErr = absolutePath(source)
	if appErr != nil {
		return nil, appErr
	}
	if info, err := os.Stat(source); err != nil || info.IsDir() {
		return nil, apperr.NotFound("导入文件不存在: "+source, "请检查文件路径", "")
	}
	if err := os.MkdirAll(filepath.Dir(location), 0o755); err != nil {
		return nil, apperr.Internal("创建导入目录失败", err.Error())
	}
	args := []string{"--import", name, location, source}
	if params.Version != 0 {
		args = append(args, "--version", strconv.Itoa(params.Version))
	}
	if params.Vhd {
		args = append(args, "--vhd")
	}
	return s.startJob("wsl/distros/import", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJob(jobCtx, job, execx.Request{Tool: execx.ToolWSL, Args: args}, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"name": name, "location": location}, nil
	})
}

// DistroUnregister implements wsl/distros/unregister.
func (s *Service) DistroUnregister(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	result, appErr := s.run(ctx, 120*time.Second, "--unregister", name)
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("卸载发行版", result); failure != nil {
		return nil, failure
	}
	return map[string]any{"name": name, "removed": true}, nil
}

// SetVersionParams is the request of wsl/distros/setVersion.
type SetVersionParams struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// DistroSetVersion implements wsl/distros/setVersion as a long running job.
func (s *Service) DistroSetVersion(ctx context.Context, emitter *dbxpluginsdk.Emitter, params SetVersionParams) (any, *apperr.Error) {
	name, appErr := validateName(params.Name)
	if appErr != nil {
		return nil, appErr
	}
	if params.Version != 1 && params.Version != 2 {
		return nil, apperr.InvalidParams("version 必须是 1 或 2", "请检查请求参数", "")
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	args := []string{"--set-version", name, strconv.Itoa(params.Version)}
	return s.startJob("wsl/distros/setVersion", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJob(jobCtx, job, execx.Request{Tool: execx.ToolWSL, Args: args}, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"name": name, "version": params.Version}, nil
	})
}

// SparseParams is the request of wsl/distros/sparse.
type SparseParams struct {
	Name   string `json:"name"`
	Sparse *bool  `json:"sparse"`
}

// DistroSparse implements wsl/distros/sparse.
func (s *Service) DistroSparse(ctx context.Context, params SparseParams) (any, *apperr.Error) {
	name, appErr := validateName(params.Name)
	if appErr != nil {
		return nil, appErr
	}
	if params.Sparse == nil {
		return nil, apperr.InvalidParams("sparse 不能为空", "请提供 true 或 false", "")
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	value := "false"
	if *params.Sparse {
		value = "true"
	}
	result, appErr := s.run(ctx, 120*time.Second, "--manage", name, "--set-sparse", value)
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("设置稀疏磁盘", result); failure != nil {
		return nil, failure
	}
	return map[string]any{"name": name, "sparse": *params.Sparse}, nil
}

// DistroCompact implements wsl/distros/compact.
func (s *Service) DistroCompact(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	entry, _, found, appErr := s.lookup(ctx, name)
	if appErr != nil {
		return nil, appErr
	}
	if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	if entry.State == "Running" {
		return nil, apperr.Conflict("发行版正在运行，无法压缩磁盘", "请先终止发行版再压缩", "")
	}
	result, appErr := s.run(ctx, 600*time.Second, "--manage", name, "--compact")
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("压缩磁盘", result); failure != nil {
		return nil, failure
	}
	return map[string]any{"name": name, "compacted": true}, nil
}

// DefaultUserParams is the request of wsl/distros/defaultUser.
type DefaultUserParams struct {
	Name string `json:"name"`
	User string `json:"user"`
}

// DistroDefaultUser implements wsl/distros/defaultUser.
func (s *Service) DistroDefaultUser(ctx context.Context, params DefaultUserParams) (any, *apperr.Error) {
	name, appErr := validateName(params.Name)
	if appErr != nil {
		return nil, appErr
	}
	user := strings.TrimSpace(params.User)
	if user == "" {
		return nil, apperr.InvalidParams("user 不能为空", "请提供默认用户名", "")
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	result, appErr := s.run(ctx, 120*time.Second, "--manage", name, "--set-default-user", user)
	if appErr != nil {
		return nil, appErr
	}
	if failure := s.launchFailure("设置默认用户", result); failure != nil {
		return nil, failure
	}
	return map[string]any{"name": name, "user": user}, nil
}

// ResizeParams is the request of wsl/distros/resize.
type ResizeParams struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// DistroResize implements wsl/distros/resize as a long running job.
func (s *Service) DistroResize(ctx context.Context, emitter *dbxpluginsdk.Emitter, params ResizeParams) (any, *apperr.Error) {
	name, appErr := validateName(params.Name)
	if appErr != nil {
		return nil, appErr
	}
	size := strings.TrimSpace(params.Size)
	if size == "" {
		return nil, apperr.InvalidParams("size 不能为空", "请提供目标大小，例如 64GB", "")
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	args := []string{"--manage", name, "--resize", size}
	return s.startJob("wsl/distros/resize", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJob(jobCtx, job, execx.Request{Tool: execx.ToolWSL, Args: args}, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"name": name, "size": size}, nil
	})
}

// ManageParams is the request of wsl/distros/manage.
type ManageParams struct {
	Name      string `json:"name"`
	Operation string `json:"operation"`
	Value     string `json:"value"`
}

// DistroManage dispatches the legacy unified operation entry point.
func (s *Service) DistroManage(ctx context.Context, emitter *dbxpluginsdk.Emitter, params ManageParams) (any, *apperr.Error) {
	operation := strings.ToLower(strings.TrimSpace(params.Operation))
	switch operation {
	case "sparse":
		value, err := strconv.ParseBool(strings.TrimSpace(params.Value))
		if err != nil {
			return nil, apperr.InvalidParams("value 必须是 true 或 false", "请检查请求参数", params.Value)
		}
		return s.DistroSparse(ctx, SparseParams{Name: params.Name, Sparse: &value})
	case "compact", "compakt":
		return s.DistroCompact(ctx, params.Name)
	case "defaultuser", "default-user", "default_user":
		return s.DistroDefaultUser(ctx, DefaultUserParams{Name: params.Name, User: params.Value})
	case "resize":
		return s.DistroResize(ctx, emitter, ResizeParams{Name: params.Name, Size: params.Value})
	default:
		return nil, apperr.InvalidParams("operation 必须是 sparse / compact / defaultUser / resize", "请检查请求参数", params.Operation)
	}
}

// DistroOpenTerminal implements wsl/distros/openTerminal.
func (s *Service) DistroOpenTerminal(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	if windowsTerminal, err := exec.LookPath("wt.exe"); err == nil {
		if startErr := winenv.StartDetached(windowsTerminal, "-w", "0", "new-tab", "wsl.exe", "-d", name); startErr == nil {
			return map[string]any{"opened": true, "terminal": "wt"}, nil
		}
	}
	commandPrompt, err := exec.LookPath("cmd.exe")
	if err != nil {
		return nil, apperr.ProcessFailed("无法打开终端", "未找到 cmd.exe 或 wt.exe", err.Error())
	}
	if startErr := winenv.StartDetached(commandPrompt, "/c", "start", "", "wsl.exe", "-d", name); startErr != nil {
		return nil, apperr.ProcessFailed("无法打开终端", "启动 cmd.exe 失败", startErr.Error())
	}
	return map[string]any{"opened": true, "terminal": "cmd"}, nil
}

// DistroOpenExplorer implements wsl/distros/openExplorer.
func (s *Service) DistroOpenExplorer(ctx context.Context, name string) (any, *apperr.Error) {
	name, appErr := validateName(name)
	if appErr != nil {
		return nil, appErr
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if !found {
		return nil, apperr.NotFound("发行版不存在: "+name, "请刷新发行版列表", "")
	}
	uri := `\\wsl.localhost\` + name
	legacy := `\\wsl$\` + name
	if !pathReachable(uri, 2*time.Second) && pathReachable(legacy, 2*time.Second) {
		uri = legacy
	}
	explorer, err := exec.LookPath("explorer.exe")
	if err != nil {
		return nil, apperr.ProcessFailed("无法打开资源管理器", "未找到 explorer.exe", err.Error())
	}
	if startErr := winenv.StartDetached(explorer, uri); startErr != nil {
		return nil, apperr.ProcessFailed("无法打开资源管理器", "启动 explorer.exe 失败", startErr.Error())
	}
	return map[string]any{"opened": true, "uri": uri}, nil
}

func pathReachable(path string, timeout time.Duration) bool {
	result := make(chan bool, 1)
	go func() {
		_, err := os.Stat(path)
		result <- err == nil
	}()
	select {
	case ok := <-result:
		return ok
	case <-time.After(timeout):
		return false
	}
}

// InstallParams is the request of wsl/distros/install.
type InstallParams struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Version  int    `json:"version"`
}

// DistroInstall implements wsl/distros/install as a long running job.
func (s *Service) DistroInstall(ctx context.Context, emitter *dbxpluginsdk.Emitter, params InstallParams) (any, *apperr.Error) {
	name, appErr := validateName(params.Name)
	if appErr != nil {
		return nil, appErr
	}
	if params.Version != 0 && params.Version != 1 && params.Version != 2 {
		return nil, apperr.InvalidParams("version 必须是 1 或 2", "请检查请求参数", "")
	}
	if _, _, found, appErr := s.lookup(ctx, name); appErr != nil {
		return nil, appErr
	} else if found {
		return nil, apperr.Conflict("发行版已存在: "+name, "请更换名称", "")
	}
	args := []string{"--install", "--distribution", name, "--no-launch"}
	if location := strings.TrimSpace(params.Location); location != "" {
		absolute, appErr := absolutePath(location)
		if appErr != nil {
			return nil, appErr
		}
		args = append(args, "--location", absolute)
	}
	if params.Version != 0 {
		args = append(args, "--version", strconv.Itoa(params.Version))
	}
	return s.startJob("wsl/distros/install", emitter, func(jobCtx context.Context, job *jobs.Job) (any, *apperr.Error) {
		_, appErr := s.streamJob(jobCtx, job, execx.Request{Tool: execx.ToolWSL, Args: args}, func(stream, text string) {
			job.Line(stream, text)
		})
		if appErr != nil {
			return nil, appErr
		}
		return map[string]any{"name": name}, nil
	})
}

// absolutePath expands environment variables and normalizes a path.
func absolutePath(value string) (string, *apperr.Error) {
	expanded := winenv.ExpandPath(strings.TrimSpace(value))
	if expanded == "" {
		return "", apperr.InvalidParams("路径不能为空", "请提供路径", "")
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", apperr.InvalidParams("路径不合法", "请提供绝对路径", err.Error())
	}
	return filepath.Clean(absolute), nil
}
