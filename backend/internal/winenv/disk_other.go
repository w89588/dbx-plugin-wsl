//go:build !windows

package winenv

import "fmt"

func FreeDiskBytes(path string) (uint64, error) {
	return 0, fmt.Errorf("Windows disk information unavailable")
}
