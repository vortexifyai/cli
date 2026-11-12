package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

const skillsMarketplaceSource = "https://github.com/vortexifyai/skills.git"
const skillsPlugin = "vortexify@vortexify-tools"

type pluginCommand struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
}

func skillInstallCommands(agent, source string) ([]pluginCommand, error) {
	if strings.TrimSpace(source) == "" || strings.HasPrefix(source, "-") {
		return nil, fmt.Errorf("source must be a marketplace Git URL, repository shorthand, or local directory")
	}
	switch agent {
	case "codex":
		return []pluginCommand{
			{"codex", []string{"plugin", "marketplace", "add", source}},
			{"codex", []string{"plugin", "add", skillsPlugin}},
		}, nil
	case "claude":
		return []pluginCommand{
			{"claude", []string{"plugin", "marketplace", "add", source}},
			{"claude", []string{"plugin", "install", skillsPlugin}},
		}, nil
	default:
		return nil, fmt.Errorf("agent must be codex or claude")
	}
}

// Keep native installation ownership with the coding tool: no private cache or
// settings-file writes, and no Vortexify project or authentication required.
func newSkillsCommand() *cobra.Command {
	return skillsCommand(func(cmd *cobra.Command, ctx context.Context, invocation pluginCommand) error {
		binary, err := exec.LookPath(invocation.Program)
		if err != nil {
			return fmt.Errorf("cannot execute coding-tool binary %q: %w; install its CLI or add it to PATH, or use --agent-path \"/absolute/path/to/binary\" to select an executable, including one bundled with a desktop app", invocation.Program, err)
		}
		process := exec.CommandContext(ctx, binary, invocation.Args...)
		process.Stdin, process.Stdout, process.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		return process.Run()
	})
}

func skillsCommand(run func(*cobra.Command, context.Context, pluginCommand) error) *cobra.Command {
	var agent, source, agentPath string
	var dryRun bool
	command := &cobra.Command{Use: "skills", Short: "Install Vortexify skills through your coding tool's plugin manager"}
	install := &cobra.Command{
		Use: "install", Short: "Install the standard Vortexify plugin for Codex or Claude Code", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			commands, err := skillInstallCommands(agent, source)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("agent-path") {
				if strings.TrimSpace(agentPath) == "" {
					return fmt.Errorf("--agent-path must name an executable CLI binary")
				}
				for i := range commands {
					commands[i].Program = agentPath
				}
			}
			if dryRun {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(commands)
			}
			for i, invocation := range commands {
				if err := run(cmd, cmd.Context(), invocation); err != nil {
					if i == 0 {
						return fmt.Errorf("could not register the Vortexify marketplace with %s: %w", agent, err)
					}
					return fmt.Errorf("marketplace registered, but plugin installation failed: %w; use %s's plugin manager to inspect or retry", err, agent)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s through %s. The plugin manager owns updates and removal.\n", skillsPlugin, agent)
			return nil
		},
	}
	install.Flags().StringVar(&agent, "agent", "", "Coding tool: codex or claude (required)")
	install.Flags().StringVar(&agentPath, "agent-path", "", "Path to the coding tool's CLI binary; defaults to finding --agent on PATH")
	install.Flags().StringVar(&source, "source", skillsMarketplaceSource, "Marketplace Git source or local agent-skills directory; remote installation requires a published source you can access")
	install.Flags().BoolVar(&dryRun, "dry-run", false, "Print native commands as JSON without installing anything")
	_ = install.MarkFlagRequired("agent")
	command.AddCommand(install)
	return command
}
