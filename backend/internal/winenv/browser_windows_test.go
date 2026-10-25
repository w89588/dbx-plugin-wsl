//go:build windows

package winenv

import (
	"os"
	"testing"
)

func TestOpenBrowserRejectsNonWebTargets(t *testing.T) {
	for _, target := range []string{"", `C:\Users`, `file:///C:/Users`, "shell:Downloads", "https://", "https://user:password@example.com", "https://example.com\x00"} {
		if err := OpenBrowser(target); err == nil {
			t.Fatalf("accepted non-web target %q", target)
		}
	}
}

// Opt-in desktop check. Confirm the actual browser address after running this;
// an accepted ShellExecute request alone does not prove the page was loaded.
func TestOpenBrowserDesktop(t *testing.T) {
	if os.Getenv("DBX_WSL_BROWSER_DESKTOP_TEST") != "1" {
		t.Skip("set DBX_WSL_BROWSER_DESKTOP_TEST=1 for a visible browser check")
	}
	if err := OpenBrowser("https://github.com/w89588/dbx-plugin-wsl/issues"); err != nil {
		t.Fatal(err)
	}
}
