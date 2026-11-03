package update

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Give the detached installer its own console and handles, so exiting the CLI
// cannot close its output or terminate the update.
func launchWindowsInstaller(args []string) error {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	args = append([]string(nil), args...)
	args[0] = filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = syscall.EscapeArg(arg)
	}
	line, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return err
	}
	startup := windows.StartupInfo{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var process windows.ProcessInformation
	if err := windows.CreateProcess(nil, line, nil, nil, false, windows.CREATE_NEW_CONSOLE, nil, nil, &startup, &process); err != nil {
		return err
	}
	windows.CloseHandle(process.Thread)
	windows.CloseHandle(process.Process)
	return nil
}
