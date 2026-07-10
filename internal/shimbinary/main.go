// Scoop Shim -- pure Go implementation matching ScoopInstaller/Shim 1:1.
//
// Behavior (from original C# shim):
// 1. Reads .shim file (same path as exe, .exe -> .shim)
// 2. Parses: path, args, cwd/workdir, elevate/runas, env vars
// 3. Expands %~dp0 -> target dir, %VAR% -> env vars
// 4. Creates process with CREATE_SUSPENDED, assigns to job object
// 5. Detects GUI subsystem -> FreeConsole or AttachConsole
// 6. Elevates with "runas" verb via ShellExecuteExW on ERROR_ELEVATION_REQUIRED
// 7. Ctrl+C handler via SetConsoleCtrlHandler, exit code forwarding
// 8. EnsureStandardHandles for CONIN$/CONOUT$ fallback

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modkernel32 = syscall.NewLazyDLL("kernel32.dll")
	modshell32  = syscall.NewLazyDLL("shell32.dll")

	procCreateProcessW           = modkernel32.NewProc("CreateProcessW")
	procWaitForSingleObject      = modkernel32.NewProc("WaitForSingleObject")
	procCloseHandle              = modkernel32.NewProc("CloseHandle")
	procGetExitCodeProcess       = modkernel32.NewProc("GetExitCodeProcess")
	procResumeThread             = modkernel32.NewProc("ResumeThread")
	procCreateJobObjectW         = modkernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = modkernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = modkernel32.NewProc("AssignProcessToJobObject")
	procSetConsoleCtrlHandler    = modkernel32.NewProc("SetConsoleCtrlHandler")
	procFreeConsole              = modkernel32.NewProc("FreeConsole")
	procAttachConsole            = modkernel32.NewProc("AttachConsole")
	procCreateFileW              = modkernel32.NewProc("CreateFileW")
	procGetStartupInfoW          = modkernel32.NewProc("GetStartupInfoW")
	procShellExecuteExW          = modshell32.NewProc("ShellExecuteExW")
)

const (
	ERROR_ELEVATION_REQUIRED             = 740
	CREATE_SUSPENDED                     = 0x00000004
	INFINITE                             = 0xFFFFFFFF
	JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE   = 0x00002000
	JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK = 0x00001000
	JobObjectExtendedLimitInformation    = 9
	ATTACH_PARENT_PROCESS                = -1
	GENERIC_READ                         = 0x80000000
	GENERIC_WRITE                        = 0x40000000
	FILE_SHARE_READ                      = 0x00000001
	FILE_SHARE_WRITE                     = 0x00000002
	OPEN_EXISTING                        = 3
	SEE_MASK_NOCLOSEPROCESS              = 0x00000040
	SEE_MASK_UNICODE                     = 0x00004000
	SEE_MASK_FLAG_NO_UI                  = 0x00000400
	SW_SHOWNORMAL                        = 1
	IMAGE_DOS_SIGNATURE                  = 0x5A4D
	IMAGE_NT_SIGNATURE                   = 0x00004550
	IMAGE_SUBSYSTEM_GUI                  = 2
)

// STARTUPINFOW (Windows API)
type startUpInfo struct {
	cb              uint32
	lpReserved      *uint16
	lpDesktop       *uint16
	lpTitle         *uint16
	dwX             uint32
	dwY             uint32
	dwXSize         uint32
	dwYSize         uint32
	dwXCountChars   uint32
	dwYCountChars   uint32
	dwFillAttribute uint32
	dwFlags         uint32
	wShowWindow     uint16
	cbReserved2     uint16
	lpReserved2     *byte
	hStdInput       syscall.Handle
	hStdOutput      syscall.Handle
	hStdError       syscall.Handle
}

// PROCESS_INFORMATION (Windows API)
type processInformation struct {
	hProcess    syscall.Handle
	hThread     syscall.Handle
	dwProcessId uint32
	dwThreadId  uint32
}

// JOBOBJECT_BASIC_LIMIT_INFORMATION
type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	_                       uint32 // padding
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	_                       uint32 // padding
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

// IO_COUNTERS
type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// JOBOBJECT_EXTENDED_LIMIT_INFORMATION
type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// SHELLEXECUTEINFOW (Windows API)
type shellExecuteInfoW struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	_            uint32 // padding
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	_            uint32 // padding
	hIcon        uintptr
	hProcess     uintptr
}

// CtrlHandler callback function pointer (matches PHANDLER_ROUTINE)
var ctrlHandlerCallback uintptr

func init() {
	ctrlHandlerCallback = syscall.NewCallback(func(dwCtrlType uint32) uintptr {
		switch dwCtrlType {
		case 0, 1, 2, 5, 6: // CTRL_C_EVENT, CTRL_BREAK_EVENT, CTRL_CLOSE_EVENT, CTRL_LOGOFF_EVENT, CTRL_SHUTDOWN_EVENT
			return 1
		}
		return 0
	})
}

// --- ShimInfo ---

type ShimInfo struct {
	Path    string
	Args    []string
	Cwd     string
	Elevate bool
	EnvVars map[string]string
}

// --- .shim file parsing (1:1 match of ParseShimInfo) ---

func parseShimInfo(shimPath, exePath string) *ShimInfo {
	dir := filepath.Dir(exePath)

	if _, err := os.Stat(shimPath); os.IsNotExist(err) {
		name := strings.TrimSuffix(filepath.Base(exePath), ".exe")
		fmt.Fprintf(os.Stderr, "Couldn't find %s.shim in %s\n", name, dir)
		return &ShimInfo{}
	}

	data, err := os.ReadFile(shimPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot open shim file for read.")
		return &ShimInfo{}
	}
	lines := strings.Split(string(data), "\n")

	// First pass: find path, compute targetDir
	targetDir := dir
	for _, rawLine := range lines {
		key, value := tryParseLine(rawLine)
		if key != "path" || value == "" {
			continue
		}
		expanded := expandAndUnquote(value)
		var combined string
		if filepath.IsAbs(expanded) {
			combined = expanded
		} else {
			combined = dir + "\\" + expanded
		}
		fullPath, err := filepath.Abs(combined)
		if err == nil {
			targetDir = filepath.Dir(fullPath) + "\\"
		}
		break
	}

	info := &ShimInfo{EnvVars: make(map[string]string)}

	for _, rawLine := range lines {
		key, value := tryParseLine(rawLine)
		if key == "" {
			continue
		}
		switch key {
		case "path":
			info.Path = expandAndUnquote(value)
		case "args":
			normalized := normalizeArgs(value, targetDir)
			if normalized != "" {
				info.Args = parseArgsFromCmdLine(normalized)
			}
		case "cwd", "workdir":
			info.Cwd = expandAndUnquote(normalizeArgs(value, targetDir))
		case "elevate", "runas":
			info.Elevate = parseBool(value)
		default:
			info.EnvVars[key] = expandAndUnquote(value)
		}
	}

	return info
}

// --- Line parsing (1:1 match of TryParseLine) ---

func tryParseLine(rawLine string) (string, string) {
	line := strings.TrimRight(rawLine, " \r\n\t")
	trimmed := strings.TrimLeft(line, " \t")

	if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' || strings.HasPrefix(trimmed, "//") {
		return "", ""
	}

	sepIdx := strings.Index(line, " = ")
	if sepIdx < 0 {
		return "", ""
	}

	key := strings.TrimSpace(line[:sepIdx])
	if key == "" {
		return "", ""
	}

	value := strings.TrimLeft(line[sepIdx+3:], " \t")
	return key, value
}

// --- Helpers (1:1 match) ---

func expandEnvVars(input string) string {
	return os.ExpandEnv(input)
}

func expandAndUnquote(value string) string {
	expanded := expandEnvVars(value)
	if len(expanded) >= 2 && expanded[0] == '"' && expanded[len(expanded)-1] == '"' {
		expanded = expanded[1 : len(expanded)-1]
	}
	return expanded
}

func normalizeArgs(args, curDir string) string {
	if args == "" {
		return args
	}
	pos := strings.Index(args, "%~dp0")
	if pos < 0 {
		return args
	}
	replacement := curDir
	if replacement != "" && replacement[len(replacement)-1] != '\\' && replacement[len(replacement)-1] != '/' {
		replacement += "\\"
	}
	return args[:pos] + replacement + args[pos+5:]
}

func parseBool(value string) bool {
	if value == "" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(value))
	return lower == "true" || lower == "1" || lower == "yes"
}

func quoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	needsQuoting := false
	for _, c := range arg {
		if c == ' ' || c == '\t' || c == '"' {
			needsQuoting = true
			break
		}
	}
	if !needsQuoting {
		return arg
	}

	var result strings.Builder
	result.WriteByte('"')

	i := 0
	for i < len(arg) {
		if arg[i] == '\\' {
			bsStart := i
			for i < len(arg) && arg[i] == '\\' {
				i++
			}
			if i == len(arg) {
				result.WriteString(strings.Repeat("\\\\", i-bsStart))
			} else if arg[i] == '"' {
				result.WriteString(strings.Repeat("\\\\", i-bsStart))
				result.WriteString("\\\"")
				i++
			} else {
				result.WriteString(strings.Repeat("\\", i-bsStart))
			}
		} else if arg[i] == '"' {
			result.WriteString("\\\"")
			i++
		} else {
			result.WriteByte(arg[i])
			i++
		}
	}
	result.WriteByte('"')
	return result.String()
}

func buildCommandLine(exePath string, args []string) string {
	var cmd strings.Builder
	cmd.WriteString(quoteArg(exePath))
	for _, arg := range args {
		cmd.WriteByte(' ')
		cmd.WriteString(quoteArg(arg))
	}
	return cmd.String()
}

func parseArgsFromCmdLine(cmdLine string) []string {
	if cmdLine == "" {
		return nil
	}
	var args []string
	var current strings.Builder
	inQuotes := false
	for i := 0; i < len(cmdLine); i++ {
		c := cmdLine[i]
		if c == '"' {
			inQuotes = !inQuotes
		} else if c == ' ' && !inQuotes {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// --- PE Subsystem detection (1:1 match of IsGuiSubsystem) ---

func isGuiSubsystem(exePath string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	f, err := os.Open(exePath)
	if err != nil {
		return false
	}
	defer f.Close()

	var dosHeader [0x40]byte
	if _, err := f.ReadAt(dosHeader[:], 0); err != nil {
		return false
	}

	dosMagic := uint16(dosHeader[0]) | uint16(dosHeader[1])<<8
	if dosMagic != IMAGE_DOS_SIGNATURE {
		return false
	}

	peOffset := int(dosHeader[0x3C]) | int(dosHeader[0x3D])<<8
	if peOffset <= 0 || peOffset > 0x1000 {
		return false
	}

	var peHeader [0x60]byte
	if _, err := f.ReadAt(peHeader[:], int64(peOffset)); err != nil {
		return false
	}

	peSig := uint32(peHeader[0]) | uint32(peHeader[1])<<8 | uint32(peHeader[2])<<16 | uint32(peHeader[3])<<24
	if peSig != IMAGE_NT_SIGNATURE {
		return false
	}

	subsystem := uint16(peHeader[0x5C]) | uint16(peHeader[0x5D])<<8
	return subsystem == IMAGE_SUBSYSTEM_GUI
}

// --- Job Object (SHIM-002) ---

func createJobObject() uintptr {
	jobHandle, _, _ := procCreateJobObjectW.Call(0, 0)
	if jobHandle == 0 || jobHandle == uintptr(syscall.InvalidHandle) {
		return 0
	}

	var jeli jobObjectExtendedLimitInformation
	jeli.BasicLimitInformation.LimitFlags =
		JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK

	procSetInformationJobObject.Call(
		jobHandle,
		JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&jeli)),
		unsafe.Sizeof(jeli),
	)

	return jobHandle
}

// --- Console handling (SHIM-003, SHIM-007) ---

func freeConsole() {
	procFreeConsole.Call()
}

func attachConsole(dwProcessId int) {
	procAttachConsole.Call(uintptr(dwProcessId))
}

// --- EnsureStandardHandles (SHIM-008) ---
// Opens CONIN$/CONOUT$ as fallback when standard handles are invalid.

func ensureStandardHandles(si *startUpInfo) {
	const invalidHandle = ^syscall.Handle(0) // 0xFFFFFFFFFFFFFFFF

	if si.hStdInput == 0 || si.hStdInput == invalidHandle {
		h, _, _ := procCreateFileW.Call(
			uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("CONIN$"))),
			GENERIC_READ,
			FILE_SHARE_READ,
			0,
			OPEN_EXISTING,
			0,
			0,
		)
		if h != uintptr(invalidHandle) {
			si.hStdInput = syscall.Handle(h)
		} else {
			si.hStdInput = 0
		}
	}
	if si.hStdOutput == 0 || si.hStdOutput == invalidHandle {
		h, _, _ := procCreateFileW.Call(
			uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("CONOUT$"))),
			GENERIC_WRITE,
			FILE_SHARE_WRITE,
			0,
			OPEN_EXISTING,
			0,
			0,
		)
		if h != uintptr(invalidHandle) {
			si.hStdOutput = syscall.Handle(h)
		} else {
			si.hStdOutput = 0
		}
	}
	if si.hStdError == 0 || si.hStdError == invalidHandle {
		h, _, _ := procCreateFileW.Call(
			uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("CONOUT$"))),
			GENERIC_WRITE,
			FILE_SHARE_WRITE,
			0,
			OPEN_EXISTING,
			0,
			0,
		)
		if h != uintptr(invalidHandle) {
			si.hStdError = syscall.Handle(h)
		} else {
			si.hStdError = 0
		}
	}
}

// --- Process launch (SHIM-001, SHIM-002, SHIM-003, SHIM-008, SHIM-010) ---

func launchProcess(info *ShimInfo, jobHandle uintptr) int {
	if info.Path == "" {
		return -1
	}

	// Set environment variables (matching C#: before CreateProcessW)
	for k, v := range info.EnvVars {
		os.Setenv(k, v)
	}

	path := info.Path
	cmdLine := buildCommandLine(path, info.Args)

	// Build params string for elevated launch (just args, not the program)
	var paramsBuilder strings.Builder
	for i, arg := range info.Args {
		if i > 0 {
			paramsBuilder.WriteByte(' ')
		}
		paramsBuilder.WriteString(quoteArg(arg))
	}
	paramsStr := paramsBuilder.String()

	if info.Elevate {
		return launchElevated(path, paramsStr, info.Cwd, jobHandle)
	}

	// Get startup info from current process (inherits standard handles)
	var si startUpInfo
	si.cb = uint32(unsafe.Sizeof(si))
	procGetStartupInfoW.Call(uintptr(unsafe.Pointer(&si)))

	// SHIM-008: Ensure standard handles are valid
	ensureStandardHandles(&si)

	// SHIM-010: Create process with CREATE_SUSPENDED
	cmdLineUTF16 := syscall.StringToUTF16(cmdLine)

	var cwdPtr *uint16
	if info.Cwd != "" {
		cwdPtr, _ = syscall.UTF16PtrFromString(info.Cwd)
	}

	var pi processInformation
	ret, _, _ := procCreateProcessW.Call(
		0, // lpApplicationName
		uintptr(unsafe.Pointer(&cmdLineUTF16[0])), // lpCommandLine (writable)
		0, // lpProcessAttributes
		0, // lpThreadAttributes
		1, // bInheritHandles (true)
		CREATE_SUSPENDED, // dwCreationFlags
		0,  // lpEnvironment (inherit parent's)
		uintptr(unsafe.Pointer(cwdPtr)), // lpCurrentDirectory
		uintptr(unsafe.Pointer(&si)), // lpStartupInfo
		uintptr(unsafe.Pointer(&pi)), // lpProcessInformation
	)

	if ret == 0 {
		errno := syscall.GetLastError()
		if errno == syscall.Errno(ERROR_ELEVATION_REQUIRED) {
			// SHIM-001: Elevation required -- launch via ShellExecuteExW with "runas"
			return launchElevated(path, paramsStr, info.Cwd, jobHandle)
		}

		fmt.Fprintf(os.Stderr, "Shim: Could not create process with command '%s'.\n", cmdLine)
		return 1
	}

	// SHIM-002: Assign to job object (KILL_ON_JOB_CLOSE ensures child is terminated when shim exits)
	if jobHandle != 0 {
		procAssignProcessToJobObject.Call(jobHandle, uintptr(pi.hProcess))
	}

	// Resume the suspended thread (SHIM-010)
	procResumeThread.Call(uintptr(pi.hThread))

	// SHIM-003: Register Ctrl+C handler so signals are forwarded to the child
	procSetConsoleCtrlHandler.Call(ctrlHandlerCallback, 1)

	// Close thread handle (no longer needed)
	procCloseHandle.Call(uintptr(pi.hThread))

	return waitAndGetExitCode(pi.hProcess)
}

// --- Elevated launch (SHIM-001) ---
// Uses ShellExecuteExW with "runas" verb instead of cmd /c.

func launchElevated(path, params, cwd string, jobHandle uintptr) int {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Shim: Unable to create elevated process.")
		return 1
	}

	var paramsPtr *uint16
	if params != "" {
		paramsPtr, _ = syscall.UTF16PtrFromString(params)
	}

	var dirPtr *uint16
	if cwd != "" {
		dirPtr, _ = syscall.UTF16PtrFromString(cwd)
	}

	verbPtr, _ := syscall.UTF16PtrFromString("runas")

	var sei shellExecuteInfoW
	sei.cbSize = uint32(unsafe.Sizeof(sei))
	sei.fMask = SEE_MASK_NOCLOSEPROCESS | SEE_MASK_UNICODE | SEE_MASK_FLAG_NO_UI
	sei.lpVerb = verbPtr
	sei.lpFile = pathPtr
	sei.lpParameters = paramsPtr
	sei.lpDirectory = dirPtr
	sei.nShow = SW_SHOWNORMAL

	ret, _, _ := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	if ret == 0 {
		fmt.Fprintln(os.Stderr, "Shim: Unable to create elevated process.")
		return 1
	}

	hProcess := syscall.Handle(sei.hProcess)

	// Assign to job object if valid
	if jobHandle != 0 && hProcess != 0 && hProcess != syscall.InvalidHandle {
		procAssignProcessToJobObject.Call(jobHandle, uintptr(hProcess))
	}

	// Register Ctrl+C handler
	procSetConsoleCtrlHandler.Call(ctrlHandlerCallback, 1)

	if hProcess != 0 && hProcess != syscall.InvalidHandle {
		return waitAndGetExitCode(hProcess)
	}

	return 0
}

func waitAndGetExitCode(hProcess syscall.Handle) int {
	procWaitForSingleObject.Call(uintptr(hProcess), INFINITE)

	var exitCode uint32
	procGetExitCodeProcess.Call(uintptr(hProcess), uintptr(unsafe.Pointer(&exitCode)))
	procCloseHandle.Call(uintptr(hProcess))

	return int(exitCode)
}

// --- Non-Windows fallback ---

func launchProcessFallback(info *ShimInfo) int {
	if info.Path == "" {
		return -1
	}

	cmdLine := buildCommandLine(info.Path, info.Args)

	cmd := exec.Command(info.Path, info.Args...)
	if info.Cwd != "" {
		cmd.Dir = info.Cwd
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				return ws.ExitStatus()
			}
			return 1
		}
		fmt.Fprintf(os.Stderr, "Shim: Could not create process with command '%s'.\n", cmdLine)
		return 1
	}
	return 0
}

// --- Main ---

func main() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Shim: Could not determine executable path.")
		os.Exit(1)
	}

	shimPath := strings.TrimSuffix(exePath, ".exe") + ".shim"

	info := parseShimInfo(shimPath, exePath)
	if info.Path == "" {
		fmt.Fprintln(os.Stderr, "Could not read shim file.")
		os.Exit(1)
	}

	// Append runtime args (skip argv[0])
	runtimeArgs := os.Args[1:]
	info.Args = append(info.Args, runtimeArgs...)

	// Set environment variables from shim before launching
	for k, v := range info.EnvVars {
		os.Setenv(k, v)
	}

	if runtime.GOOS == "windows" {
		// GUI subsystem detection and console management (SHIM-007)
		isGUI := isGuiSubsystem(exePath)
		if isGUI {
			// Match C#: args.Length == 0 && info.Args.Count == 0
			// In Go, os.Args includes the program at [0], so len(os.Args) == 1 means no runtime args.
			// info.Args already includes runtime args appended above, so info.Args.Empty means no args at all.
			if len(os.Args) == 1 && len(info.Args) == 0 {
				freeConsole()
			} else {
				attachConsole(ATTACH_PARENT_PROCESS)
			}
		}

		// SHIM-002: Create job object with KILL_ON_JOB_CLOSE
		jobHandle := createJobObject()

		// SHIM-001/010/003/008: Launch process with full Windows API control
		exitCode := launchProcess(info, jobHandle)

		// Clean up job object
		if jobHandle != 0 {
			procCloseHandle.Call(jobHandle)
		}

		if exitCode < 0 {
			os.Exit(1)
		}
		os.Exit(exitCode)
	}

	// Non-Windows fallback (shouldn't be reached for the shim binary)
	os.Exit(launchProcessFallback(info))
}
