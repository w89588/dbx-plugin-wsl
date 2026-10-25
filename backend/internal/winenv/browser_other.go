//go:build !windows

package winenv

import "fmt"

func OpenBrowser(address string) error {
	return fmt.Errorf("opening the default browser is only supported on Windows")
}
