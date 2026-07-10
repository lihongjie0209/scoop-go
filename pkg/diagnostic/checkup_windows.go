//go:build windows

package diagnostic

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// isLongPathsEnabled checks whether long path support is enabled in the Windows registry.
// Reads HKLM\System\CurrentControlSet\Control\FileSystem\LongPathsEnabled.
func isLongPathsEnabled() (bool, error) {
	val, err := readRegistryDWORD(syscall.HKEY_LOCAL_MACHINE,
		`System\CurrentControlSet\Control\FileSystem`, "LongPathsEnabled")
	if err != nil {
		return false, fmt.Errorf("cannot read registry: %w", err)
	}
	return val == 1, nil
}

// isDeveloperModeEnabled checks whether Developer Mode is enabled in the Windows registry.
// Reads HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock\AllowDevelopmentWithoutDevLicense.
func isDeveloperModeEnabled() (bool, error) {
	val, err := readRegistryDWORD(syscall.HKEY_LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock`,
		"AllowDevelopmentWithoutDevLicense")
	if err != nil {
		return false, fmt.Errorf("cannot read registry: %w", err)
	}
	return val == 1, nil
}

// checkWindowsDefender checks if Windows Defender is running and the scoop directory
// is in Defender's exclusion list.
func checkWindowsDefender() Check {
	c := Check{Name: "Windows Defender"}

	scoopDir := app.Dirs().ScoopDir

	running, err := isDefenderServiceRunning()
	if err != nil {
		c.Passed = false
		c.Message = "Windows Defender: " + err.Error()
		c.Fix = "Ensure Windows Defender is properly configured"
		return c
	}

	if !running {
		c.Passed = false
		c.Message = "Windows Defender is not running"
		c.Fix = "Start Windows Defender or install an alternative antivirus"
		return c
	}

	excluded, err := isPathInDefenderExclusion(scoopDir)
	if err != nil {
		c.Passed = true
		c.Message = "Windows Defender is running but exclusion list could not be verified: " + err.Error()
		return c
	}

	if excluded {
		c.Passed = true
		c.Message = "Windows Defender is running and scoop directory is excluded"
	} else {
		c.Passed = false
		c.Message = "Windows Defender is running but scoop directory is not excluded"
		c.Fix = fmt.Sprintf("Run: Add-MpPreference -ExclusionPath '%s'", scoopDir)
	}

	return c
}

// isDefenderServiceRunning checks if the WinDefend service is in the RUNNING state.
func isDefenderServiceRunning() (bool, error) {
	cmd := exec.Command("sc", "query", "WinDefend")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to query Windows Defender service: %w", err)
	}
	return bytes.Contains(out, []byte("RUNNING")), nil
}

// isPathInDefenderExclusion checks whether the given path is covered by a Windows Defender
// exclusion. It enumerates values under HKLM\SOFTWARE\Microsoft\Windows Defender\Exclusions\Paths.
func isPathInDefenderExclusion(path string) (bool, error) {
	subKey := `SOFTWARE\Microsoft\Windows Defender\Exclusions\Paths`

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, subKey, registry.QUERY_VALUE)
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND || err == syscall.ERROR_PATH_NOT_FOUND {
			return false, nil
		}
		return false, fmt.Errorf("opening registry key: %w", err)
	}
	defer k.Close()

	valueNames, err := k.ReadValueNames(-1)
	if err != nil {
		return false, fmt.Errorf("reading value names: %w", err)
	}

	cleanPath := filepath.Clean(path)

	for _, excludedPath := range valueNames {
		excludedClean := filepath.Clean(excludedPath)

		// Case-insensitive comparison on Windows
		if strings.EqualFold(cleanPath, excludedClean) {
			return true, nil
		}

		// Also check if scoop dir is a subdirectory of an excluded path
		// e.g. exclusion is C:\Users and scoop dir is C:\Users\foo\scoop
		prefix := strings.ToLower(excludedClean) + string(filepath.Separator)
		if strings.HasPrefix(strings.ToLower(cleanPath), prefix) {
			return true, nil
		}
	}

	return false, nil
}

// checkNtfsVolume checks that scoopdir and globaldir are on NTFS volumes.
func checkNtfsVolume() Check {
	c := Check{Name: "NTFS volume"}

	type namedPath struct {
		name string
		path string
	}

	dirs := []namedPath{
		{"scoopdir", app.Dirs().ScoopDir},
		{"globaldir", app.Dirs().GlobalDir},
	}

	// Deduplicate paths to avoid checking the same volume twice
	seen := make(map[string]bool)
	var allNtfs = true
	var issues []string

	for _, d := range dirs {
		if d.path == "" || seen[d.path] {
			continue
		}
		seen[d.path] = true

		fs, err := getVolumeFileSystem(d.path)
		if err != nil {
			c.Passed = false
			c.Message = "NTFS volume check failed: " + err.Error()
			return c
		}
		if !strings.EqualFold(fs, "NTFS") {
			allNtfs = false
			issues = append(issues, fmt.Sprintf("%s (%s) is on a %s volume", d.name, d.path, fs))
		}
	}

	if allNtfs {
		c.Passed = true
		c.Message = "Scoop directories are on NTFS volumes"
	} else {
		c.Passed = false
		c.Message = strings.Join(issues, "; ")
		c.Fix = "Scoop requires NTFS volumes for junction support. Move scoop to an NTFS drive."
	}

	return c
}

// getVolumeFileSystem returns the file system name (e.g. "NTFS", "FAT32") for the volume
// containing the given path.
func getVolumeFileSystem(p string) (string, error) {
	rootPath := filepath.VolumeName(p) + string(filepath.Separator)

	rootPtr, err := syscall.UTF16PtrFromString(rootPath)
	if err != nil {
		return "", err
	}

	var fsName [windows.MAX_PATH + 1]uint16
	err = windows.GetVolumeInformation(
		rootPtr,
		nil,
		0,
		nil,
		nil,
		nil,
		&fsName[0],
		uint32(len(fsName)),
	)
	if err != nil {
		return "", fmt.Errorf("getting volume information for %s: %w", rootPath, err)
	}

	return windows.UTF16ToString(fsName[:]), nil
}

// checkHelperTools checks for the availability of common helper tools (innounp, dark, lessmsi).
func checkHelperTools() Check {
	c := Check{Name: "Helper tools"}

	tools := []struct {
		name string
		pkg  string
	}{
		{"innounp.exe", "innounp"},
		{"dark.exe", "dark"},
		{"lessmsi.exe", "lessmsi"},
	}

	var missing []string
	for _, tool := range tools {
		if _, err := exec.LookPath(tool.name); err != nil {
			missing = append(missing, tool.pkg)
		}
	}

	if len(missing) == 0 {
		c.Passed = true
		c.Message = "All helper tools are available"
	} else {
		c.Passed = false
		c.Message = fmt.Sprintf("Missing helper tools: %s", strings.Join(missing, ", "))
		var fixes []string
		for _, m := range missing {
			fixes = append(fixes, fmt.Sprintf("scoop install %s", m))
		}
		c.Fix = "Run: " + strings.Join(fixes, " && ")
	}

	return c
}

// readRegistryDWORD reads a REG_DWORD value from the Windows registry.
func readRegistryDWORD(rootKey syscall.Handle, subKey, valueName string) (uint32, error) {
	var key syscall.Handle
	err := syscall.RegOpenKeyEx(rootKey,
		syscall.StringToUTF16Ptr(subKey),
		0, syscall.KEY_READ, &key)
	if err != nil {
		return 0, fmt.Errorf("opening registry key: %w", err)
	}
	defer syscall.RegCloseKey(key)

	var value uint32
	var valueType uint32
	var bufSize uint32 = 4
	err = syscall.RegQueryValueEx(key,
		syscall.StringToUTF16Ptr(valueName),
		nil, &valueType,
		(*byte)(unsafe.Pointer(&value)),
		&bufSize)
	if err != nil {
		return 0, fmt.Errorf("reading registry value: %w", err)
	}
	return value, nil
}
