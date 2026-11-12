package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSkillsNativeInstall(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			var got []pluginCommand
			cmd := skillsCommand(func(_ *cobra.Command, _ context.Context, invocation pluginCommand) error {
				got = append(got, invocation)
				return nil
			})
			cmd.SetOut(&bytes.Buffer{})
			source := "/a project/marketplace;literal"
			cmd.SetArgs([]string{"install", "--agent", agent, "--source", source})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			action := "add"
			if agent == "claude" {
				action = "install"
			}
			want := []pluginCommand{{agent, []string{"plugin", "marketplace", "add", source}}, {agent, []string{"plugin", action, skillsPlugin}}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}
}

func TestSkillsStopsOnNativeFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		calls := 0
		cmd := skillsCommand(func(_ *cobra.Command, _ context.Context, _ pluginCommand) error {
			calls++
			if calls == failAt {
				return errors.New("native failure")
			}
			return nil
		})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs([]string{"install", "--agent", "codex"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "native failure") || calls != failAt {
			t.Fatalf("calls=%d, err=%v", calls, err)
		}
		if failAt == 2 && !strings.Contains(err.Error(), "marketplace registered") {
			t.Fatal(err)
		}
	}
}

func TestSkillsDryRunAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"install"}, {"install", "--agent", "unknown"},
		{"install", "--agent", "codex", "--source", ""},
		{"install", "--agent", "codex", "--source=--help"},
		{"install", "--agent", "claude", "--agent-path", ""},
		{"install", "--agent", "claude", "--agent-path", "   "},
	} {
		cmd := skillsCommand(func(_ *cobra.Command, _ context.Context, _ pluginCommand) error {
			t.Fatal("invalid install ran")
			return nil
		})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if cmd.Execute() == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	var output bytes.Buffer
	cmd := skillsCommand(func(_ *cobra.Command, _ context.Context, _ pluginCommand) error {
		t.Fatal("dry run installed")
		return nil
	})
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"install", "--agent", "codex", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var commands []pluginCommand
	if err := json.Unmarshal(output.Bytes(), &commands); err != nil || len(commands) != 2 {
		t.Fatalf("%s: %v", output.String(), err)
	}
}

func TestSkillsExecutableSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX executable script")
	}
	for _, agent := range []string{"codex", "claude"} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%t", agent, explicit), func(t *testing.T) {
				dir := t.TempDir()
				binary := filepath.Join(dir, "bundled "+agent)
				if !explicit {
					binary = filepath.Join(dir, agent)
				}
				if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$VORTEXIFY_TEST_INVOCATIONS\"\n"), 0755); err != nil {
					t.Fatal(err)
				}
				output := filepath.Join(dir, "invocations")
				t.Setenv("VORTEXIFY_TEST_INVOCATIONS", output)
				t.Setenv("PATH", dir)
				args := []string{"install", "--agent", agent, "--source", "/marketplace with spaces;literal"}
				if explicit {
					t.Setenv("PATH", "")
					args = append(args, "--agent-path", binary)
				}
				cmd := newSkillsCommand()
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				action := "add"
				if agent == "claude" {
					action = "install"
				}
				want := "plugin\nmarketplace\nadd\n/marketplace with spaces;literal\nplugin\n" + action + "\n" + skillsPlugin + "\n"
				if string(got) != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			})
		}
	}
}

func TestSkillsMissingExecutableExplainsOverride(t *testing.T) {
	t.Setenv("PATH", "")
	for _, override := range []string{"", filepath.Join(t.TempDir(), "missing binary")} {
		cmd := newSkillsCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		args := []string{"install", "--agent", "claude"}
		if override != "" {
			args = append(args, "--agent-path", override)
		}
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--agent-path") || !strings.Contains(err.Error(), "PATH") {
			t.Fatalf("expected actionable missing-binary error, got %v", err)
		}
	}
}

func TestSkillsDryRunPrintsExplicitPathWithoutResolvingIt(t *testing.T) {
	t.Setenv("PATH", "")
	var output bytes.Buffer
	cmd := newSkillsCommand()
	cmd.SetOut(&output)
	binary := filepath.Join(t.TempDir(), "uninstalled cli")
	cmd.SetArgs([]string{"install", "--agent", "claude", "--agent-path", binary, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var commands []pluginCommand
	if err := json.Unmarshal(output.Bytes(), &commands); err != nil || len(commands) != 2 {
		t.Fatalf("%s: %v", output.String(), err)
	}
	for _, invocation := range commands {
		if invocation.Program != binary {
			t.Fatalf("dry run ignored --agent-path: %#v", invocation)
		}
	}
}
