//go:build windows

package env

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	advapi32              = syscall.NewLazyDLL("advapi32.dll")
	procRegSetValueExW    = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW   = advapi32.NewProc("RegDeleteValueW")

	user32                = syscall.NewLazyDLL("user32.dll")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

// writeEnvVarWindows writes an environment variable to the Windows registry.
// Uses REG_EXPAND_SZ if the value contains '%', otherwise REG_SZ.
// The registry key is HKCU\Environment for user scope or
// HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment for global scope.
func writeEnvVarWindows(name, value string, global bool) error {
	var rootKey syscall.Handle
	var subKey string

	if global {
		rootKey = syscall.HKEY_LOCAL_MACHINE
		subKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	} else {
		rootKey = syscall.HKEY_CURRENT_USER
		subKey = "Environment"
	}

	var key syscall.Handle
	err := syscall.RegOpenKeyEx(rootKey,
		syscall.StringToUTF16Ptr(subKey),
		0, syscall.KEY_SET_VALUE, &key)
	if err != nil {
		return fmt.Errorf("opening registry key %s: %w", subKey, err)
	}
	defer syscall.RegCloseKey(key)

	if value == "" {
		// Delete the value from registry
		ret, _, _ := procRegDeleteValueW.Call(
			uintptr(key),
			uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(name))),
		)
		if ret != 0 {
			errno := syscall.Errno(ret)
			if errno != syscall.ERROR_FILE_NOT_FOUND {
				return fmt.Errorf("deleting registry value %s: %w", name, errno)
			}
		}
		return nil
	}

	// Detect value kind: REG_EXPAND_SZ if value contains %VAR% references, otherwise REG_SZ
	valueType := uintptr(syscall.REG_SZ)
	if strings.Contains(value, "%") {
		valueType = syscall.REG_EXPAND_SZ
	}

	// Write the value as UTF-16 little-endian
	valueUTF16 := syscall.StringToUTF16(value)
	ret, _, _ := procRegSetValueExW.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(name))),
		0,
		valueType,
		uintptr(unsafe.Pointer(&valueUTF16[0])),
		uintptr(len(valueUTF16)*2),
	)
	if ret != 0 {
		return fmt.Errorf("setting registry value %s: %w", name, syscall.Errno(ret))
	}

	return nil
}

// readEnvFromRegistry reads an environment variable from the Windows registry.
func readEnvFromRegistry(name string, global bool) (string, error) {
	var rootKey syscall.Handle
	var subKey string

	if global {
		rootKey = syscall.HKEY_LOCAL_MACHINE
		subKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	} else {
		rootKey = syscall.HKEY_CURRENT_USER
		subKey = "Environment"
	}

	var key syscall.Handle
	err := syscall.RegOpenKeyEx(rootKey,
		syscall.StringToUTF16Ptr(subKey),
		0, syscall.KEY_READ, &key)
	if err != nil {
		return "", fmt.Errorf("opening registry key %s: %w", subKey, err)
	}
	defer syscall.RegCloseKey(key)

	// First query the buffer size needed
	var bufSize uint32
	var valueType uint32
	err = syscall.RegQueryValueEx(key,
		syscall.StringToUTF16Ptr(name),
		nil, &valueType, nil, &bufSize)
	if err != nil {
		return "", fmt.Errorf("querying registry value %s size: %w", name, err)
	}

	if bufSize == 0 {
		return "", nil
	}

	// Read the value data
	buf := make([]byte, bufSize)
	err = syscall.RegQueryValueEx(key,
		syscall.StringToUTF16Ptr(name),
		nil, &valueType,
		&buf[0],
		&bufSize)
	if err != nil {
		return "", fmt.Errorf("reading registry value %s: %w", name, err)
	}

	// Decode UTF-16LE to string
	u16s := make([]uint16, len(buf)/2)
	for i := range u16s {
		u16s[i] = uint16(buf[i*2]) | uint16(buf[i*2+1])<<8
	}
	return syscall.UTF16ToString(u16s), nil
}

// publishEnvChangeWindows broadcasts a WM_SETTINGCHANGE message to notify
// running applications that environment variables have changed.
func publishEnvChangeWindows() error {
	const HWND_BROADCAST = 0xFFFF
	const WM_SETTINGCHANGE = 0x001A
	const SMTO_ABORTIFHUNG = 0x0002

	lParam := syscall.StringToUTF16Ptr("Environment")

	ret, _, err := procSendMessageTimeoutW.Call(
		uintptr(HWND_BROADCAST),
		uintptr(WM_SETTINGCHANGE),
		0,
		uintptr(unsafe.Pointer(lParam)),
		uintptr(SMTO_ABORTIFHUNG),
		5000,
		0,
	)
	if ret == 0 {
		return fmt.Errorf("SendMessageTimeoutW failed: %w", err)
	}

	return nil
}
