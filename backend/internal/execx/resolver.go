package execx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tool identifies one of the external executables the backend drives.
type Tool string

const (
	ToolWSL  Tool = "wsl"
	ToolWSLc Tool = "wslc"
)

// ResolveTTL is the contract mandated tool discovery cache duration.
const ResolveTTL = 5 * time.Minute

// ToolNotFoundError is returned when an executable cannot be located.
type ToolNotFoundError struct {
	Tool       Tool
	Candidates []string
}

func (e *ToolNotFoundError) Error() string {
	return fmt.Sprintf("%s.exe was not found", e.Tool)
}

type resolvedTool struct {
	path      string
	checkedAt time.Time
	err       error
}

// Resolver locates wsl.exe and wslc.exe following the order in contract
// section 6.1 and caches the outcome for five minutes.
type Resolver struct {
	mutex    sync.Mutex
	cache    map[Tool]resolvedTool
	ttl      time.Duration
	now      func() time.Time
	lookPath func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
}

// NewResolver returns a resolver using the real environment.
func NewResolver() *Resolver {
	return &Resolver{
		cache:    map[Tool]resolvedTool{},
		ttl:      ResolveTTL,
		now:      time.Now,
		lookPath: exec.LookPath,
		stat:     os.Stat,
	}
}

// Resolve returns the executable path for a tool, caching the outcome.
func (r *Resolver) Resolve(tool Tool) (string, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if cached, ok := r.cache[tool]; ok && r.now().Sub(cached.checkedAt) < r.ttl {
		return cached.path, cached.err
	}
	candidates := r.Candidates(tool)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := r.stat(candidate)
		if err == nil && !info.IsDir() {
			r.cache[tool] = resolvedTool{path: candidate, checkedAt: r.now()}
			return candidate, nil
		}
	}
	err := &ToolNotFoundError{Tool: tool, Candidates: candidates}
	r.cache[tool] = resolvedTool{checkedAt: r.now(), err: err}
	return "", err
}

// Candidates returns the ordered lookup candidates for a tool. Every candidate
// is an absolute path; PATH probing is resolved to a concrete path first.
func (r *Resolver) Candidates(tool Tool) []string {
	var candidates []string
	add := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for _, existing := range candidates {
			if strings.EqualFold(existing, path) {
				return
			}
		}
		candidates = append(candidates, path)
	}
	switch tool {
	case ToolWSL:
		add(filepath.Join(systemRoot(), "System32", "wsl.exe"))
		if path, err := r.lookPath("wsl.exe"); err == nil {
			add(path)
		}
		add(filepath.Join(localAppData(), "Microsoft", "WindowsApps", "wsl.exe"))
	case ToolWSLc:
		if path, err := r.lookPath("wslc.exe"); err == nil {
			add(path)
		}
		add(filepath.Join(programFiles(), "WSL", "wslc.exe"))
		add(filepath.Join(localAppData(), "Microsoft", "WindowsApps", "wslc.exe"))
		add(`C:\Program Files\WSL\wslc.exe`)
	}
	return candidates
}

func systemRoot() string {
	if value := os.Getenv("SystemRoot"); value != "" {
		return value
	}
	return `C:\Windows`
}

func programFiles() string {
	if value := os.Getenv("ProgramFiles"); value != "" {
		return value
	}
	return `C:\Program Files`
}

func localAppData() string {
	if value := os.Getenv("LOCALAPPDATA"); value != "" {
		return value
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "AppData", "Local")
	}
	return ""
}
