// Package winenv exposes the small amount of Windows specific information the
// backend needs (registered WSL distributions, OS version, detached process
// launching) behind build tags so the other packages stay portable.
package winenv

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

var envPattern = regexp.MustCompile(`%([^%]+)%`)

// Username returns the current Windows user name.
func Username() string {
	if value := strings.TrimSpace(os.Getenv("USERNAME")); value != "" {
		return value
	}
	if current, err := user.Current(); err == nil {
		return current.Username
	}
	return ""
}

// Hostname returns the machine name.
func Hostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	return hostname
}

// HomeDir returns the current user's profile directory.
func HomeDir() string {
	if value := strings.TrimSpace(os.Getenv("USERPROFILE")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// LocalAppData returns %LOCALAPPDATA%.
func LocalAppData() string {
	if value := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); value != "" {
		return value
	}
	return filepath.Join(HomeDir(), "AppData", "Local")
}

// ProgramFiles returns %ProgramFiles%.
func ProgramFiles() string {
	if value := strings.TrimSpace(os.Getenv("ProgramFiles")); value != "" {
		return value
	}
	return `C:\Program Files`
}

// ExpandPath expands %VAR% references and a leading ~ in user supplied paths.
func ExpandPath(value string) string {
	expanded := envPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := match[1 : len(match)-1]
		if replacement := os.Getenv(name); replacement != "" {
			return replacement
		}
		return match
	})
	home := HomeDir()
	if expanded == "~" {
		return home
	}
	if strings.HasPrefix(expanded, `~\`) || strings.HasPrefix(expanded, "~/") {
		return filepath.Join(home, expanded[2:])
	}
	return expanded
}
