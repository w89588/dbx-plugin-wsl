//go:build windows

package winenv

import (
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const lxssRegistryPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Lxss`

// LxssDistro is one registered distribution from HKCU Lxss.
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

// ReadLxss reads the WSL distribution registry. Errors are returned when the
// key cannot be opened so callers can degrade gracefully.
func ReadLxss() (Lxss, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var info Lxss
	root, err := openRegistryKey(syscall.HKEY_CURRENT_USER, lxssRegistryPath)
	if err != nil {
		return info, err
	}
	defer syscall.RegCloseKey(root) //nolint:errcheck

	if value, ok := queryStringValue(root, "DefaultDistribution"); ok {
		info.DefaultDistributionGUID = value
	}
	index := uint32(0)
	for {
		var nameBuffer [256]uint16
		nameLength := uint32(len(nameBuffer))
		err := syscall.RegEnumKeyEx(root, index, &nameBuffer[0], &nameLength, nil, nil, nil, nil)
		if err != nil {
			break
		}
		index++
		guid := syscall.UTF16ToString(nameBuffer[:nameLength])
		subkey, err := openRegistryKey(root, guid)
		if err != nil {
			continue
		}
		distro := LxssDistro{GUID: guid}
		if value, ok := queryStringValue(subkey, "DistributionName"); ok {
			distro.DistributionName = value
		}
		if value, ok := queryStringValue(subkey, "BasePath"); ok {
			distro.BasePath = value
		}
		if value, ok := queryDWORDValue(subkey, "Version"); ok {
			distro.Version = int(value)
		}
		if value, ok := queryDWORDValue(subkey, "DefaultUid"); ok {
			distro.DefaultUid = int(value)
		}
		if value, ok := queryDWORDValue(subkey, "Flags"); ok {
			distro.Flags = int(value)
		}
		if value, ok := queryDWORDValue(subkey, "State"); ok {
			distro.State = int(value)
		}
		syscall.RegCloseKey(subkey) //nolint:errcheck
		if distro.DistributionName != "" {
			info.Distros = append(info.Distros, distro)
		}
	}
	return info, nil
}

func openRegistryKey(parent syscall.Handle, path string) (syscall.Handle, error) {
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var handle syscall.Handle
	if err := syscall.RegOpenKeyEx(parent, pathPointer, 0, syscall.KEY_READ|syscall.KEY_WOW64_64KEY, &handle); err != nil {
		return 0, err
	}
	return handle, nil
}

func queryStringValue(key syscall.Handle, name string) (string, bool) {
	namePointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false
	}
	var valueType uint32
	var size uint32
	if err := syscall.RegQueryValueEx(key, namePointer, nil, &valueType, nil, &size); err != nil || size == 0 {
		return "", false
	}
	buffer := make([]byte, size+2)
	if err := syscall.RegQueryValueEx(key, namePointer, nil, &valueType, &buffer[0], &size); err != nil {
		return "", false
	}
	switch valueType {
	case syscall.REG_SZ, syscall.REG_EXPAND_SZ:
		units := (*[1 << 20]uint16)(unsafe.Pointer(&buffer[0]))[: size/2 : size/2]
		return syscall.UTF16ToString(units), true
	default:
		return "", false
	}
}

func queryDWORDValue(key syscall.Handle, name string) (uint32, bool) {
	namePointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, false
	}
	var valueType uint32
	var value uint32
	size := uint32(unsafe.Sizeof(value))
	if err := syscall.RegQueryValueEx(key, namePointer, nil, &valueType, (*byte)(unsafe.Pointer(&value)), &size); err != nil {
		return 0, false
	}
	if valueType != syscall.REG_DWORD {
		return 0, false
	}
	return value, true
}

// WindowsVersion returns the Windows build in the form 10.0.<build>.<ubr>,
// matching the value WSL itself reports.
func WindowsVersion() string {
	key, err := openRegistryKey(syscall.HKEY_LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`)
	if err != nil {
		return ""
	}
	defer syscall.RegCloseKey(key) //nolint:errcheck

	major, hasMajor := queryDWORDValue(key, "CurrentMajorVersionNumber")
	minor, _ := queryDWORDValue(key, "CurrentMinorVersionNumber")
	build, _ := queryStringValue(key, "CurrentBuildNumber")
	ubr, _ := queryDWORDValue(key, "UBR")
	if !hasMajor {
		major = 10
	}
	parts := []string{
		strconv.FormatUint(uint64(major), 10),
		strconv.FormatUint(uint64(minor), 10),
	}
	if strings.TrimSpace(build) != "" {
		parts = append(parts, strings.TrimSpace(build))
	}
	if ubr > 0 {
		parts = append(parts, strconv.FormatUint(uint64(ubr), 10))
	}
	return strings.Join(parts, ".")
}
