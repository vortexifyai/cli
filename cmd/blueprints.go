package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"vortexify-cli/internal/api"
)

type templateFile struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	Entrypoint string   `json:"entrypoint"`
	Release    string   `json:"release"`
	Files      []string `json:"files"`
	Content    string   `json:"content"`
	Encoding   string   `json:"encoding"`
}

func readTemplate(cmd *cobra.Command, client *api.Client, projectID, id, path string) (*templateFile, error) {
	values := map[string]any{"template_id": id}
	if path != "" {
		values["path"] = path
	}
	response, err := client.Service(cmd.Context(), "artifacts/templates/read", projectID, values)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var file templateFile
	if err := json.NewDecoder(response.Body).Decode(&file); err != nil {
		return nil, err
	}
	return &file, nil
}

func (f *templateFile) bytes() ([]byte, error) {
	switch f.Encoding {
	case "utf-8":
		return []byte(f.Content), nil
	case "base64":
		return base64.StdEncoding.DecodeString(f.Content)
	default:
		return nil, fmt.Errorf("unsupported template file encoding %q", f.Encoding)
	}
}

func safeTemplatePath(path string) bool {
	if path == "" || strings.ContainsAny(path, "\\\x00") || filepath.IsAbs(path) {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func previewTemplate(cmd *cobra.Command, client *api.Client, projectID, id string) error {
	first, err := readTemplate(cmd, client, projectID, id, "")
	if err != nil {
		return err
	}
	if !safeTemplatePath(first.Path) {
		return fmt.Errorf("invalid template entrypoint")
	}
	directory, err := os.MkdirTemp("", "vortexify-blueprint-")
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			os.RemoveAll(directory)
		}
	}()
	paths := append([]string{first.Path}, first.Files...)
	written := map[string]bool{}
	for _, path := range paths {
		if written[path] {
			continue
		}
		if !safeTemplatePath(path) {
			return fmt.Errorf("invalid template file path %q", path)
		}
		file := first
		if path != first.Path {
			file, err = readTemplate(cmd, client, projectID, id, path)
			if err != nil {
				return err
			}
			if file.Release != first.Release || file.Path != path {
				return fmt.Errorf("template release changed while downloading; retry preview")
			}
		}
		data, err := file.bytes()
		if err != nil {
			return err
		}
		destination := filepath.Join(directory, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			return err
		}
		written[path] = true
	}
	completed = true
	_, err = fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(directory, filepath.FromSlash(first.Path)))
	return err
}

func newBlueprintsCommand(opts *options) *cobra.Command {
	run := func(action string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			client := api.NewClient(opts.apiURL)
			projectID := ""
			if action == "search" {
				if err := selectOrganization(cmd, client, opts.orgID); err != nil {
					return err
				}
			} else {
				_, cfg, scoped, err := workspace(opts)
				if err != nil {
					return err
				}
				client, projectID = scoped, cfg.ProjectID
				if err := client.ProjectOrg(cmd.Context(), projectID); err != nil {
					return err
				}
			}
			if action == "preview" {
				return previewTemplate(cmd, client, projectID, args[0])
			}
			values := map[string]any{}
			endpoint := "attached"
			switch action {
			case "search":
				endpoint = "search"
				values["query"] = strings.Join(args, " ")
				values["include_answer"] = true
				conversationID, _ := cmd.Flags().GetString("conversation")
				if conversationID != "" {
					values["conversation_id"] = conversationID
				}
				limit, _ := cmd.Flags().GetInt("limit")
				if limit < 1 || limit > 20 {
					return fmt.Errorf("limit must be from 1 to 20")
				}
				values["limit"] = limit
				for _, flag := range []string{"industry", "solution-type", "use-case", "category", "subcategory", "module", "subindustry"} {
					value, _ := cmd.Flags().GetString(flag)
					if value != "" {
						values[strings.ReplaceAll(flag, "-", "_")] = value
					}
				}
			case "metadata":
				endpoint = "metadata"
				values["template_id"] = args[0]
			case "read", "code":
				endpoint = "read"
				values["template_id"] = args[0]
				if len(args) > 1 {
					values["path"] = args[1]
				}
			case "attach", "detach":
				endpoint = "attach"
				values["template_id"] = args[0]
				values["attached"] = action == "attach"
			}
			service := client.Service
			if action == "search" {
				service = client.DiscoveryService
			}
			response, err := service(cmd.Context(), "artifacts/templates/"+endpoint, projectID, values)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			raw, _ := cmd.Flags().GetBool("raw")
			output, _ := cmd.Flags().GetString("output")
			if raw || output != "" {
				var file templateFile
				if err := json.NewDecoder(response.Body).Decode(&file); err != nil {
					return err
				}
				data, err := file.bytes()
				if err != nil {
					return err
				}
				if output != "" {
					return os.WriteFile(output, data, 0600)
				}
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			var result any
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
				return err
			}
			if object, ok := result.(map[string]any); ok {
				for _, key := range []string{"templates", "designs"} {
					if value, found := object[key]; found {
						object["blueprints"] = value
						delete(object, key)
					}
				}
				if value, found := object["template"]; found {
					object["blueprint"] = value
					delete(object, "template")
				}
			}
			asYAML, _ := cmd.Flags().GetBool("yaml")
			if asYAML {
				return yaml.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(result)
		}
	}
	cmd := &cobra.Command{Use: "blueprints", Short: "Search blueprints and manage project attachments", Args: cobra.NoArgs, RunE: run("attached")}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List attached project blueprints", Args: cobra.NoArgs, RunE: run("attached")})
	search := &cobra.Command{Use: "search <query>", Short: "Search the full blueprint catalog", Args: cobra.MinimumNArgs(1), RunE: run("search")}
	search.Flags().String("conversation", "", "Continue a saved search conversation; omit to start a new search")
	for _, flag := range []string{"category", "subcategory", "module", "subindustry"} {
		search.Flags().String(flag, "", "Filter by "+flag)
	}
	search.Flags().Int("limit", 10, "Maximum matching blueprints (1–20)")
	search.Flags().String("industry", "", "Filter by industry")
	search.Flags().String("solution-type", "", "Filter by solution type")
	search.Flags().String("use-case", "", "Filter by use case")
	search.Flags().Bool("yaml", false, "Print structured metadata as YAML")
	cmd.AddCommand(search)
	metadata := &cobra.Command{Use: "metadata <blueprint-name>", Short: "Read blueprint metadata and available files", Args: cobra.ExactArgs(1), RunE: run("metadata")}
	metadata.Flags().Bool("yaml", false, "Print blueprint metadata as YAML")
	cmd.AddCommand(metadata)
	for _, action := range []string{"read", "code"} {
		sub := &cobra.Command{Use: action + " <blueprint-name> [path]", Short: "Read source or README without attaching the blueprint", Args: cobra.RangeArgs(1, 2), RunE: run(action)}
		sub.Flags().Bool("raw", action == "code", "Write decoded source instead of the JSON file response")
		sub.Flags().StringP("output", "o", "", "Write decoded file contents to this local path")
		cmd.AddCommand(sub)
	}
	cmd.AddCommand(&cobra.Command{Use: "preview <blueprint-name>", Short: "Download blueprint files into a temporary folder and print its HTML path", Args: cobra.ExactArgs(1), RunE: run("preview")})
	for _, action := range []string{"attach", "detach"} {
		short := "Attach a blueprint to this project"
		if action == "detach" {
			short = "Detach a blueprint from this project"
		}
		cmd.AddCommand(&cobra.Command{Use: action + " <blueprint-name>", Short: short, Args: cobra.ExactArgs(1), RunE: run(action)})
	}
	return cmd
}
