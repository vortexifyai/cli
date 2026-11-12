package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"time"
)

func newGuardianCommand(opts *options) *cobra.Command {
	var wait, asJSON, retry bool
	var timeout time.Duration
	command := &cobra.Command{Use: "guardian", Short: "Inspect project guardian readiness", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		if timeout <= 0 {
			return fmt.Errorf("timeout must be positive")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		if retry {
			if err := client.ActivateGuardian(ctx, cfg.ProjectID); err != nil {
				return err
			}
		}
		previous := ""
		for {
			status, err := client.GetGuardianStatus(ctx, cfg.ProjectID)
			if err != nil {
				return err
			}
			if !asJSON && status.Status != previous {
				fmt.Fprintf(cmd.OutOrStdout(), "Project guardian: %s\n", status.Status)
				if status.Error != nil && *status.Error != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Error: %s\n", *status.Error)
				}
				previous = status.Status
			}
			if !wait || status.Status != "activating" {
				if asJSON {
					if err := json.NewEncoder(cmd.OutOrStdout()).Encode(status); err != nil {
						return err
					}
				}
				if status.Status == "failed" {
					return fmt.Errorf("guardian activation failed; use vortexify guardian --retry --wait")
				}
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
	}}
	command.Flags().BoolVar(&wait, "wait", false, "Wait for project guardian activation")
	command.Flags().BoolVar(&retry, "retry", false, "Retry project guardian activation")
	command.Flags().BoolVar(&asJSON, "json", false, "Print structured JSON")
	command.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum wait; does not cancel activation")
	return command
}
