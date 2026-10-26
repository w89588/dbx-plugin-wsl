//go:build !windows

package winenv

import "fmt"

func SelectFolder() (string, error) { return "", fmt.Errorf("Windows only") }
