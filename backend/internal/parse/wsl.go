// Package parse converts raw wsl.exe text output and wslc JSON output into the
// normalized structures described by docs/rpc-contract.md.
package parse

import (
	"regexp"
	"strconv"
	"strings"
)

var multiSpace = regexp.MustCompile(`\s{2,}`)

// Version holds the fields printed by `wsl --version`.
type Version struct {
	WSL      string
	Kernel   string
	WSLg     string
	MSRDC    string
	Direct3D string
	DXCore   string
	Windows  string
}

// Status holds the fields printed by `wsl --status`.
type Status struct {
	DefaultDistro  string
	DefaultVersion int
	WSL1Supported  bool
	Raw            string
}

// DistroEntry is one row of `wsl --list --verbose`.
type DistroEntry struct {
	Name      string
	State     string
	Version   int
	IsDefault bool
}

// OnlineDistro is one row of `wsl --list --online`.
type OnlineDistro struct {
	Name         string
	FriendlyName string
}

// SessionEntry is one row of `wslc system session list`.
type SessionEntry struct {
	ID          int
	CreatorPid  int
	DisplayName string
}

// splitKeyValue splits a localized "Key: value" line.
func splitKeyValue(line string) (string, string, bool) {
	line = strings.TrimSpace(strings.TrimRight(line, "\r"))
	if line == "" {
		return "", "", false
	}
	index := strings.IndexAny(line, ":：")
	if index < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:index])
	value := strings.TrimSpace(line[index+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

// ParseVersion parses `wsl --version` output (localized keys are supported).
func ParseVersion(output string) Version {
	var version Version
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := splitKeyValue(line)
		if !ok {
			continue
		}
		lowered := strings.ToLower(key)
		switch {
		case strings.Contains(lowered, "wslg"):
			version.WSLg = value
		case strings.Contains(lowered, "kernel") || strings.Contains(key, "内核"):
			version.Kernel = value
		case strings.Contains(lowered, "msrdc"):
			version.MSRDC = value
		case strings.Contains(lowered, "direct3d"):
			version.Direct3D = value
		case strings.Contains(lowered, "dxcore"):
			version.DXCore = value
		case strings.Contains(lowered, "windows") || strings.Contains(key, "windows"):
			version.Windows = value
		case strings.Contains(lowered, "wsl") || strings.Contains(key, "版本"):
			version.WSL = value
		}
	}
	return version
}

// ParseStatus parses `wsl --status` output (Chinese and English).
func ParseStatus(output string) Status {
	status := Status{Raw: output, WSL1Supported: strings.TrimSpace(output) != ""}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := splitKeyValue(line)
		if !ok {
			continue
		}
		lowered := strings.ToLower(key)
		switch {
		case strings.Contains(key, "默认分发") || strings.Contains(lowered, "default distribution"):
			status.DefaultDistro = value
		case strings.Contains(key, "默认版本") || strings.Contains(lowered, "default version"):
			if parsed, err := strconv.Atoi(value); err == nil {
				status.DefaultVersion = parsed
			}
		}
	}
	lowered := strings.ToLower(output)
	if strings.Contains(lowered, "wsl1") || strings.Contains(lowered, "wsl 1") || strings.Contains(output, "WSL1") {
		if strings.Contains(output, "不支持") || strings.Contains(lowered, "not supported") ||
			strings.Contains(output, "未启用") || strings.Contains(lowered, "not enabled") {
			status.WSL1Supported = false
		}
	}
	return status
}

// ParseDistroList parses `wsl --list --verbose`.
func ParseDistroList(output string) []DistroEntry {
	lines := strings.Split(output, "\n")
	headerIndex := -1
	for index, line := range lines {
		fields := strings.Fields(line)
		hasName, hasState := false, false
		for _, field := range fields {
			switch strings.ToUpper(field) {
			case "NAME":
				hasName = true
			case "STATE":
				hasState = true
			}
		}
		if hasName && hasState {
			headerIndex = index
			break
		}
	}
	if headerIndex < 0 {
		return nil
	}
	var entries []DistroEntry
	for _, line := range lines[headerIndex+1:] {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" {
			continue
		}
		isDefault := false
		if strings.HasPrefix(trimmed, "*") {
			isDefault = true
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "*"))
		}
		fields := multiSpace.Split(trimmed, -1)
		compact := fields[:0]
		for _, field := range fields {
			if strings.TrimSpace(field) != "" {
				compact = append(compact, strings.TrimSpace(field))
			}
		}
		if len(compact) < 2 {
			continue
		}
		entry := DistroEntry{
			Name:      compact[0],
			State:     NormalizeDistroState(compact[1]),
			IsDefault: isDefault,
		}
		if len(compact) > 2 {
			if version, err := strconv.Atoi(compact[2]); err == nil {
				entry.Version = version
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// NormalizeDistroState maps localized states to the contract values.
func NormalizeDistroState(value string) string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	switch {
	case lowered == "running" || strings.Contains(value, "运行中"):
		return "Running"
	case lowered == "stopped" || strings.Contains(value, "已停止"):
		return "Stopped"
	case lowered == "installing" || strings.Contains(value, "安装中"):
		return "Installing"
	case lowered == "uninstalling" || strings.Contains(value, "卸载中"):
		return "Uninstalling"
	default:
		return "Unknown"
	}
}

// ParseOnlineList parses `wsl --list --online`.
func ParseOnlineList(output string) []OnlineDistro {
	lines := strings.Split(output, "\n")
	headerIndex := -1
	friendlyIndex := -1
	for index, line := range lines {
		if friendlyIndex = strings.Index(strings.ToUpper(line), "FRIENDLY NAME"); friendlyIndex >= 0 {
			headerIndex = index
			break
		}
	}
	if headerIndex < 0 {
		return nil
	}
	var entries []OnlineDistro
	for _, line := range lines[headerIndex+1:] {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || len(line) <= friendlyIndex {
			continue
		}
		name := strings.TrimSpace(line[:friendlyIndex])
		friendly := strings.TrimSpace(line[friendlyIndex:])
		if name == "" {
			continue
		}
		entries = append(entries, OnlineDistro{Name: name, FriendlyName: friendly})
	}
	return entries
}

// ParseSessions parses the table printed by `wslc system session list`.
func ParseSessions(output string) []SessionEntry {
	var entries []SessionEntry
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" {
			continue
		}
		lowered := strings.ToLower(trimmed)
		if strings.Contains(lowered, "显示名称") || strings.Contains(lowered, "display name") ||
			(strings.Contains(trimmed, "PID") && strings.Contains(trimmed, "ID")) {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 3 {
			continue
		}
		id, errID := strconv.Atoi(fields[0])
		pid, errPid := strconv.Atoi(fields[1])
		if errID != nil || errPid != nil {
			continue
		}
		entries = append(entries, SessionEntry{
			ID:          id,
			CreatorPid:  pid,
			DisplayName: strings.Join(fields[2:], " "),
		})
	}
	return entries
}

// ParseVersionText extracts a version string from `wslc version` ("wslc 3.0.3.0").
func ParseVersionText(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			continue
		}
		lowered := strings.ToLower(line)
		index := strings.Index(lowered, "wslc")
		if index >= 0 {
			rest := strings.TrimSpace(line[index+len("wslc"):])
			rest = strings.TrimPrefix(rest, "版本:")
			rest = strings.TrimPrefix(rest, "version")
			rest = strings.TrimSpace(rest)
			if rest != "" {
				return rest
			}
		}
	}
	return ""
}
