package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
