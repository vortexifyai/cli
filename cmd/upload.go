package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/resources"
)

func newUploadCommand(opts *options) *cobra.Command {
	command := &cobra.Command{Use: "upload", Short: "Upload tables or attach documents to a connector or agent"}
	for _, kind := range []string{"table", "file"} {
		command.AddCommand(newDataUploadCommand(opts, kind))
	}
	return command
}

func newDataUploadCommand(opts *options, kind string) *cobra.Command {
	var name, resourceType, resource string
	var wait, asJSON bool
	var timeout time.Duration
	command := &cobra.Command{Use: kind + " <path>", Short: "Upload a local " + kind + " and start ingestion", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if wait && timeout <= 0 {
			return fmt.Errorf("timeout must be positive")
		}
		ext := strings.ToLower(filepath.Ext(args[0]))
		supported := map[string]bool{".csv": true, ".tsv": true, ".xlsx": true}
		if kind == "file" {
			supported = map[string]bool{".pdf": true, ".txt": true, ".md": true, ".markdown": true}
		}
		if !supported[ext] {
			return fmt.Errorf("unsupported %s extension %q", kind, ext)
		}
		file, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("upload path must be a nonempty regular file")
		}
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		ownerID := ""
		if kind == "file" {
			if resourceType != "" && resourceType != "connector" && resourceType != "agent" {
				return fmt.Errorf("resource-type must be connector or agent")
			}
			rows, err := client.GetResourceGraph(cmd.Context(), cfg.ProjectID)
			if err != nil {
				return err
			}
			owner, err := selectUploadOwner(cmd, rows, resourceType, resource, asJSON)
			if err != nil {
				return err
			}
			resourceType, ownerID = owner.Type, owner.ID
		}
		result, err := client.UploadData(cmd.Context(), cfg.ProjectID, info.Name(), file, info.Size(), kind, name, resourceType, ownerID)
		if err != nil {
			return err
		}
		jobID := result["job_id"].(string)
		if wait {
			result, err = waitForJobDisplay(cmd, client, cfg.ProjectID, jobID, timeout, asJSON)
			if err != nil {
				return err
			}
		}
		if err = printExecution(cmd.OutOrStdout(), result, asJSON); err != nil {
			return err
		}
		return executionFailure(result)
	}}
	command.Flags().StringVar(&name, "name", "", "Table or attachment name (defaults to the filename without its extension)")
	command.Flags().BoolVar(&wait, "wait", false, "Wait for ingestion to finish")
	command.Flags().BoolVar(&asJSON, "json", false, "Print structured JSON; file uploads require an explicit resource")
	command.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Maximum polling duration; does not cancel ingestion")
	if kind == "file" {
		command.Flags().StringVar(&resourceType, "resource-type", "", "Attachment owner type: connector or agent")
		command.Flags().StringVar(&resource, "resource", "", "Attachment owner name or ID (otherwise select interactively)")
	}
	return command
}

func selectUploadOwner(cmd *cobra.Command, rows []resources.Registered, kind, selector string, asJSON bool) (resources.Registered, error) {
	choices := []resources.Registered{}
	for _, row := range rows {
		if (row.Type == "connector" || row.Type == "agent") && (kind == "" || row.Type == kind) && (selector == "" || row.ID == selector || row.Name == selector) {
			choices = append(choices, row)
		}
	}
	if len(choices) == 0 {
		return resources.Registered{}, fmt.Errorf("no matching connector or agent; create the attachment owner first")
	}
	if selector != "" {
		if len(choices) != 1 {
			return resources.Registered{}, fmt.Errorf("ambiguous attachment owner; specify --resource-type or use its ID")
		}
		return choices[0], nil
	}
	if asJSON {
		return resources.Registered{}, fmt.Errorf("use --resource (and --resource-type if ambiguous) with --json")
	}
	for i, row := range choices {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d. %s %s (%s)\n", i+1, row.Type, row.Name, row.ID)
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Attach file to resource number: ")
	scanner := bufio.NewScanner(cmd.InOrStdin())
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return resources.Registered{}, err
		}
		return resources.Registered{}, fmt.Errorf("no resource selected; use --resource for noninteractive uploads")
	}
	index, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || index < 1 || index > len(choices) {
		return resources.Registered{}, fmt.Errorf("invalid resource selection")
	}
	return choices[index-1], nil
}
