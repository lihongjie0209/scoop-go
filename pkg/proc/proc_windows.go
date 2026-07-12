//go:build windows

package proc

import (
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ListProcessPaths returns full executable paths for running processes.
func ListProcessPaths() ([]string, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, err
	}

	var paths []string
	for {
		pid := pe.ProcessID
		if path, err := processImagePath(pid); err == nil && path != "" {
			paths = append(paths, path)
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return paths, nil
}

// ListProcessImages returns base names of running process executables.
func ListProcessImages() ([]string, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, err
	}

	var images []string
	for {
		name := windows.UTF16ToString(pe.ExeFile[:])
		if name != "" {
			images = append(images, name)
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return images, nil
}

func processImagePath(pid uint32) (string, error) {
	if pid == 0 {
		return "", syscall.EINVAL
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	var buf [windows.MAX_PATH + 1]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return filepath.Clean(windows.UTF16ToString(buf[:size])), nil
}
