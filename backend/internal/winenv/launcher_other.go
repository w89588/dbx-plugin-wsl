//go:build !windows

package winenv

import (
	"os/exec"
)

// LxssDistro is one registered distribution from the Windows registry.
type LxssDistro struct {
	GUID             string
	DistributionName string
	BasePath         string
	Version          int
	DefaultUid       int
	Flags            int
	State            int
}

// Lxss describes the registered distribution registry state.
type Lxss struct {
	DefaultDistributionGUID string
	Distros                 []LxssDistro
}

// ReadLxss is unsupported outside Windows.
func ReadLxss() (Lxss, error) {
	return Lxss{}, nil
}

// WindowsVersion is unsupported outside Windows.
func WindowsVersion() string { return "" }

// StartDetached launches a process without blocking on it.
func StartDetached(path string, args ...string) error {
	command := exec.Command(path, args...)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
