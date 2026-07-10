//go:build windows

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fsctlSetReparsePoint   = 0x000900A4
	ioReparseTagMountPoint = 0xA0000003
)

// reparseDataBuffer is the header of the Windows REPARSE_DATA_BUFFER
// structure used for a mount-point (junction) reparse point.
// sizeof(header) = 16 bytes. The PathBuffer follows immediately.
type reparseDataBuffer struct {
	ReparseTag           uint32
	ReparseDataLength    uint16
	Reserved             uint16
	SubstituteNameOffset uint16
	SubstituteNameLength uint16
	PrintNameOffset      uint16
	PrintNameLength      uint16
}

// createJunction creates a directory junction (reparse point) on Windows
// using FSCTL_SET_REPARSE_POINT. Junctions work without Developer Mode or
// admin privileges, unlike directory symlinks.
//
// The link argument is the junction path to create, target is the directory
// it should point to.
func createJunction(link, target string) error {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	// The target directory must exist
	if _, err := os.Stat(absTarget); err != nil {
		return fmt.Errorf("junction target does not exist: %w", err)
	}

	// Remove existing path at the junction location
	if _, err := os.Stat(link); err == nil {
		if err := os.RemoveAll(link); err != nil {
			return fmt.Errorf("removing existing path for junction: %w", err)
		}
	}

	// Ensure parent directory chain exists
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		return err
	}

	// Create the junction directory (must exist to set a reparse point on it)
	if err := os.Mkdir(link, 0755); err != nil {
		return fmt.Errorf("creating junction source directory: %w", err)
	}

	// Open the directory with FILE_FLAG_OPEN_REPARSE_POINT so we can write
	// the reparse data without following any existing reparse point.
	linkPtr, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}

	h, err := windows.CreateFile(
		linkPtr,
		windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return fmt.Errorf("opening junction source directory: %w", err)
	}
	defer windows.CloseHandle(h)

	// The substitute name MUST be in NT namespace: \??\<absolute-path>
	substName := `\??\` + absTarget
	printName := absTarget

	substUTF16, err := windows.UTF16FromString(substName)
	if err != nil {
		return err
	}
	printUTF16, err := windows.UTF16FromString(printName)
	if err != nil {
		return err
	}

	// Byte lengths of UTF-16 strings including null terminators
	substBytes := len(substUTF16) * 2
	printBytes := len(printUTF16) * 2

	// Allocate the full reparse data buffer: header + path data
	headerSize := int(unsafe.Sizeof(reparseDataBuffer{}))
	bufSize := headerSize + substBytes + printBytes
	buf := make([]byte, bufSize)

	// Populate the reparse data header
	header := (*reparseDataBuffer)(unsafe.Pointer(&buf[0]))
	header.ReparseTag = ioReparseTagMountPoint
	// ReparseDataLength is the size after the Reserved field:
	// 4 WORD fields (8 bytes) + path buffer
	header.ReparseDataLength = uint16(8 + substBytes + printBytes)
	header.SubstituteNameOffset = 0
	// Lengths exclude null terminators per Windows spec
	header.SubstituteNameLength = uint16(substBytes - 2)
	// Print name immediately follows substitute name + its null terminator
	header.PrintNameOffset = uint16(substBytes)
	header.PrintNameLength = uint16(printBytes - 2)

	// Copy UTF-16 path data into the PathBuffer area
	srcSubst := unsafe.Slice((*byte)(unsafe.Pointer(&substUTF16[0])), substBytes)
	srcPrint := unsafe.Slice((*byte)(unsafe.Pointer(&printUTF16[0])), printBytes)

	copy(buf[headerSize:headerSize+substBytes], srcSubst)
	copy(buf[headerSize+substBytes:], srcPrint)

	// Issue FSCTL_SET_REPARSE_POINT to create the junction
	var bytesReturned uint32
	if err := windows.DeviceIoControl(
		h,
		fsctlSetReparsePoint,
		&buf[0],
		uint32(bufSize),
		nil,
		0,
		&bytesReturned,
		nil,
	); err != nil {
		return fmt.Errorf("setting junction reparse point: %w", err)
	}

	return nil
}

// setJunctionReadOnly sets the read-only attribute on a junction reparse point
// using attrib +R /L. The /L flag ensures the attribute is applied to the
// reparse point itself, not the target directory.
func setJunctionReadOnly(path string) error {
	cmd := exec.Command("attrib", "+R", "/L", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("setting +R attribute on junction: %w\nOutput: %s", err, string(output))
	}
	return nil
}
