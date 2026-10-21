//go:build windows

package winenv

import (
	"os/exec"
	"syscall"
)

// StartDetached launches a process without blocking on it. It is used for
// terminals and File Explorer (contract 4.2 openTerminal/openExplorer).
func StartDetached(path string, args ...string) error {
	command := exec.Command(path, args...)
	command.SysProcAttr = &syscall.SysProcAttr{}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
