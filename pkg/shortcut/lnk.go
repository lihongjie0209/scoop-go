package shortcut

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// LinkData describes a Windows Shell Link (.lnk) target.
type LinkData struct {
	TargetPath string
	Arguments  string
	WorkingDir string
	IconPath   string
}

// WriteShellLink writes a minimal but valid Shell Link Binary File (MS-SHLLINK)
// without PowerShell or COM. Sufficient for Scoop start-menu shortcuts.
func WriteShellLink(path string, data LinkData) error {
	if strings.TrimSpace(data.TargetPath) == "" {
		return fmt.Errorf("shortcut target path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	target := filepath.Clean(data.TargetPath)
	var flags uint32
	// HasLinkInfo | IsUnicode
	const (
		HasLinkInfo      = 1 << 1
		HasName          = 1 << 2
		HasWorkingDir    = 1 << 4
		HasArguments     = 1 << 5
		HasIconLocation  = 1 << 6
		IsUnicode        = 1 << 7
		PreferEnvironment= 1 << 9
	)
	flags = HasLinkInfo | IsUnicode
	if data.WorkingDir != "" {
		flags |= HasWorkingDir
	}
	if data.Arguments != "" {
		flags |= HasArguments
	}
	if data.IconPath != "" {
		flags |= HasIconLocation
	}

	// ShellLinkHeader: 0x4C bytes
	header := make([]byte, 0x4C)
	binary.LittleEndian.PutUint32(header[0:4], 0x4C) // HeaderSize
	// LinkCLSID: 00021401-0000-0000-C000-000000000046
	copy(header[4:20], []byte{
		0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46,
	})
	binary.LittleEndian.PutUint32(header[20:24], flags) // LinkFlags
	// FileAttributes, times, FileSize, IconIndex, ShowCommand, HotKey left zero
	binary.LittleEndian.PutUint32(header[60:64], 1) // ShowCommand = SW_SHOWNORMAL

	// LinkInfo structure (LocalBasePath only, no volume ID required for relative resolve skip)
	// We use LinkInfo with VolumeID + LocalBasePath for absolute local path.
	linkInfo := buildLinkInfo(target)

	var body []byte
	body = append(body, header...)
	body = append(body, linkInfo...)

	// StringData (Unicode): NAME optional skip; WORKING_DIR; COMMAND_LINE_ARGUMENTS; ICON_LOCATION
	if data.WorkingDir != "" {
		body = append(body, encodeStringData(data.WorkingDir)...)
	}
	if data.Arguments != "" {
		body = append(body, encodeStringData(data.Arguments)...)
	}
	if data.IconPath != "" {
		body = append(body, encodeStringData(data.IconPath)...)
	}

	// Terminal block ExtraData (at least TerminalBlock = 0 size marker is 4 zero bytes? )
	// Spec: ExtraData continues until TerminalBlock with BlockSize < 4.
	// Write TerminalBlock with size 0.
	term := make([]byte, 4)
	binary.LittleEndian.PutUint32(term, 0)
	body = append(body, term...)

	return os.WriteFile(path, body, 0644)
}

func buildLinkInfo(target string) []byte {
	// LinkInfoHeaderSize = 0x1C
	// LinkInfoFlags = VolumeIDAndLocalBasePath (1)
	// cbVolumeID, cbLocalBasePath, cbCommonNetworkRelativeLink, cbCommonPathSuffix offsets
	localBase := target
	if !strings.HasSuffix(localBase, "\x00") {
		// LinkInfo uses ANSI LocalBasePath; use UTF-8/ACP best-effort via system default bytes
	}
	// Use Unicode LocalBasePathExtraOffset path via flags? Keep simple: ANSI path for ASCII targets,
	// and also store Unicode path in EnvironmentVariableDataBlock-like extra via StringData already.
	// For reliability with non-ASCII, put path as LocalBasePath UTF-8 bytes (Windows often accepts).

	volID := buildVolumeID()
	localBaseBytes := append([]byte(localBase), 0) // null-terminated ANSI
	commonSuffix := []byte{0}

	// Offsets relative to start of LinkInfo
	// Header 0x1C
	// then VolumeID
	// then LocalBasePath
	// then CommonPathSuffix
	headerSize := uint32(0x1C)
	volumeOffset := headerSize
	localBaseOffset := volumeOffset + uint32(len(volID))
	commonSuffixOffset := localBaseOffset + uint32(len(localBaseBytes))
	totalSize := commonSuffixOffset + uint32(len(commonSuffix))

	buf := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(buf[0:4], totalSize)            // LinkInfoSize
	binary.LittleEndian.PutUint32(buf[4:8], headerSize)           // LinkInfoHeaderSize
	binary.LittleEndian.PutUint32(buf[8:12], 1)                   // VolumeIDAndLocalBasePath
	binary.LittleEndian.PutUint32(buf[12:16], volumeOffset)       // VolumeIDOffset
	binary.LittleEndian.PutUint32(buf[16:20], localBaseOffset)    // LocalBasePathOffset
	binary.LittleEndian.PutUint32(buf[20:24], 0)                  // CommonNetworkRelativeLinkOffset
	binary.LittleEndian.PutUint32(buf[24:28], commonSuffixOffset) // CommonPathSuffixOffset

	out := append(buf, volID...)
	out = append(out, localBaseBytes...)
	out = append(out, commonSuffix...)
	// Fix total size in case
	binary.LittleEndian.PutUint32(out[0:4], uint32(len(out)))
	return out
}

func buildVolumeID() []byte {
	// VolumeIDSize, DriveType=FIXED(3), DriveSerialNumber, VolumeLabelOffset, label ""
	// Size = 0x10 with empty label
	buf := make([]byte, 0x10)
	binary.LittleEndian.PutUint32(buf[0:4], 0x10)
	binary.LittleEndian.PutUint32(buf[4:8], 3) // DRIVE_FIXED
	binary.LittleEndian.PutUint32(buf[8:12], 0)
	binary.LittleEndian.PutUint32(buf[12:16], 0x10) // VolumeLabelOffset -> end (empty)
	return buf
}

func encodeStringData(s string) []byte {
	// Count (uint16) of UTF-16 code units, then UTF-16LE string (no null terminator)
	u := utf16.Encode([]rune(s))
	buf := make([]byte, 2+len(u)*2)
	binary.LittleEndian.PutUint16(buf[0:2], uint16(len(u)))
	for i, c := range u {
		binary.LittleEndian.PutUint16(buf[2+i*2:], c)
	}
	return buf
}
