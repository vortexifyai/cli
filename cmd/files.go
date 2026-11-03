package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"strings"
	"unicode/utf8"
	"vortexify-cli/internal/api"
)

func newFilesCommand(opts *options) *cobra.Command {
	var chars, version int
	var search string
	var limit int
	var asJSON bool
	command := &cobra.Command{Use: "files <agent|connector> <name>", Short: "List or search uploaded files attached to an agent or connector", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "agent" && args[0] != "connector" {
			return fmt.Errorf("file owner must be agent or connector")
		}
		if chars < 1 || chars > 4000 {
			return fmt.Errorf("chars must be between 1 and 4000")
		}
		if version < 0 || (cmd.Flags().Changed("version") && version == 0) {
			return fmt.Errorf("version must be positive")
		}
		if cmd.Flags().Changed("search") && (strings.TrimSpace(search) == "" || utf8.RuneCountInString(search) > 2000) {
			return fmt.Errorf("search must contain 1–2000 characters")
		}
		if limit < 1 || limit > 100 {
			return fmt.Errorf("limit must be between 1 and 100")
		}
		if cmd.Flags().Changed("limit") && !cmd.Flags().Changed("search") {
			return fmt.Errorf("--limit requires --search")
		}
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		rows, err := client.ListRegisteredResources(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		id := ""
		for _, row := range rows {
			if row.Type == args[0] && (row.ID == args[1] || row.Name == args[1]) {
				if id != "" {
					return fmt.Errorf("ambiguous resource; use its ID")
				}
				id = row.ID
			}
		}
		if id == "" {
			return fmt.Errorf("attachment owner not found")
		}
		if cmd.Flags().Changed("search") {
			matches, err := client.SearchResourceFiles(cmd.Context(), cfg.ProjectID, args[0], id, search, limit, version)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(matches)
			}
			if len(matches) == 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No matching passages in this resource's uploaded files.")
				return err
			}
			for _, match := range matches {
				location := ""
				if match.Page != nil {
					location = fmt.Sprintf(" — page %d", *match.Page)
				}
				if match.Section != "" {
					location += " — " + match.Section
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n%s\n\n", match.Name, location, match.Text); err != nil {
					return err
				}
			}
			return nil
		}
		result, err := client.GetResourceFilePreviews(cmd.Context(), cfg.ProjectID, args[0], id, chars, version)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		for _, file := range result.Files {
			if err := printFilePreview(cmd, file); err != nil {
				return err
			}
		}
		if len(result.Files) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No uploaded files attached to this resource.")
		}
		if result.Omitted > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "%d more files omitted from bounded previews.\n", result.Omitted)
		}
		return nil
	}}
	command.Flags().IntVar(&chars, "chars", 1000, "Maximum characters per file (1–4000; combined previews capped at 10000 characters)")
	command.Flags().IntVar(&version, "version", 0, "Committed owner version (defaults to working attachments)")
	command.Flags().StringVar(&search, "search", "", "Search the contents of this resource's uploaded files")
	command.Flags().IntVar(&limit, "limit", 10, "Maximum matching passages for --search (1–100)")
	command.Flags().BoolVar(&asJSON, "json", false, "Print structured uploaded-file previews or search matches")
	var previewChars int
	var previewJSON bool
	preview := &cobra.Command{Use: "preview <file-id>", Short: "Read a bounded beginning of an uploaded document", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if previewChars < 1 || previewChars > 4000 {
			return fmt.Errorf("chars must be between 1 and 4000")
		}
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		result, err := client.GetFilePreview(cmd.Context(), cfg.ProjectID, args[0], previewChars)
		if err != nil {
			return err
		}
		if previewJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		return printFilePreview(cmd, *result)
	}}
	preview.Flags().IntVar(&previewChars, "chars", 1000, "Maximum characters (1–4000)")
	preview.Flags().BoolVar(&previewJSON, "json", false, "Print structured file preview")
	command.AddCommand(preview)
	return command
}

func printFilePreview(cmd *cobra.Command, file api.FilePreview) error {
	suffix := ""
	if file.Truncated {
		suffix = " (truncated; use --search for further details)"
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n%s\n\n", file.Name, suffix, file.Text)
	return err
}
