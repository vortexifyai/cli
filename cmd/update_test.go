package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"vortexify-cli/internal/update"
)

type fakeUpdater struct {
	latest       string
	failure      error
	installs     int
	pending      bool
	installError error
}

func (f *fakeUpdater) Latest(context.Context) (update.Release, error) {
	return update.Release{Tag: f.latest}, f.failure
}
func (f *fakeUpdater) Install(context.Context, update.Release, string, io.Writer, io.Writer) (bool, error) {
	f.installs++
	return f.pending, f.installError
}

func TestUpdateCommand(t *testing.T) {
	for _, tc := range []struct {
		name, installed, latest, text string
		check, pending, wantError     bool
		installs                      int
	}{
		{"check newer", "v0.1.9", "v0.1.10", "Run vortexify update", true, false, false, 0},
		{"already current", "v0.1.0", "v0.1.0", "No newer", false, false, false, 0},
		{"no downgrade", "v1.0.0", "v0.1.0", "No newer", false, false, false, 0},
		{"update", "v0.1.0", "v0.1.1", "Updated Vortexify", false, false, false, 1},
		{"Windows update", "v0.1.0", "v0.1.1", "separate PowerShell window", false, true, false, 1},
		{"check dev", "dev", "v0.1.1", "development build", true, false, false, 0},
		{"preserve dev", "dev", "v0.1.1", "", false, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updater := &fakeUpdater{latest: tc.latest, pending: tc.pending}
			command := updateCommand(tc.installed, updater, func() (string, error) {
				if tc.installs == 0 {
					t.Fatal("check resolved executable path")
				}
				return "/installed/vortexify", nil
			})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			if tc.check {
				command.SetArgs([]string{"--check"})
			} else {
				command.SetArgs([]string{})
			}
			err := command.Execute()
			if (err != nil) != tc.wantError || updater.installs != tc.installs || !strings.Contains(output.String(), tc.text) {
				t.Fatalf("error=%v, installs=%d, output=%s", err, updater.installs, output.String())
			}
		})
	}
}

func TestUpdateLookupFailureDoesNotInstall(t *testing.T) {
	updater := &fakeUpdater{failure: fmt.Errorf("GitHub unavailable")}
	command := updateCommand("v0.1.0", updater, func() (string, error) { t.Fatal("resolved executable after failed lookup"); return "", nil })
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--check"})
	if err := command.Execute(); err == nil || updater.installs != 0 {
		t.Fatal(err, updater.installs)
	}
}

func TestUpdateInstallerFailureDoesNotReportSuccess(t *testing.T) {
	updater := &fakeUpdater{latest: "v0.1.1", installError: fmt.Errorf("installer failed")}
	command := updateCommand("v0.1.0", updater, func() (string, error) { return "/installed/vortexify", nil })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{})
	if err := command.Execute(); err == nil || updater.installs != 1 || strings.Contains(output.String(), "Updated Vortexify") {
		t.Fatalf("error=%v, installs=%d, output=%s", err, updater.installs, output.String())
	}
}
