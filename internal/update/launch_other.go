//go:build !windows

package update

import "fmt"

func launchWindowsInstaller([]string) error {
	return fmt.Errorf("Windows installer requires Windows")
}
