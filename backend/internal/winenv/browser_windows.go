//go:build windows

package winenv

import (
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// OpenBrowser hands an HTTPS URL to the Windows default protocol handler.
// Explorer's command line is for filesystem navigation and can open a folder
// instead of the requested page. Do not route web links through StartDetached.
func OpenBrowser(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || strings.ContainsAny(address, "\r\n\x00\\") {
		return fmt.Errorf("expected an absolute HTTPS URL")
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	target, err := syscall.UTF16PtrFromString(address)
	if err != nil {
		return err
	}
	// Shell handlers may require a COM STA. Both COM calls must use the same
	// OS thread; normal Go goroutines can move between threads.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ole32 := syscall.NewLazyDLL("ole32.dll")
	hresult, _, _ := ole32.NewProc("CoInitializeEx").Call(0, 0x2|0x4)
	if int32(hresult) < 0 {
		return fmt.Errorf("initialize browser handler: HRESULT 0x%08x", uint32(hresult))
	}
	defer ole32.NewProc("CoUninitialize").Call()
	result, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(
		0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1,
	)
	runtime.KeepAlive(verb)
	runtime.KeepAlive(target)
	if result <= 32 {
		return fmt.Errorf("Windows browser launch failed (ShellExecute code %d)", result)
	}
	return nil
}
