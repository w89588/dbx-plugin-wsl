//go:build windows

package winenv

import (
	"context"
	"encoding/base64"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func SelectFolder() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	script := `Add-Type -AssemblyName System.Windows.Forms; $dialog=New-Object System.Windows.Forms.FolderBrowserDialog; $dialog.Description='WSL Manager'; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($dialog.SelectedPath)) }; $dialog.Dispose()`
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-WindowStyle", "Hidden", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	raw, err := cmd.Output()
	if err != nil {
		return "", err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	return string(decoded), err
}
