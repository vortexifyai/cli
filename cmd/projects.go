package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/api"
)

// Organization selection is per invocation; no choice is saved as a default.
func selectOrganization(cmd *cobra.Command, client *api.Client, explicit string) error {
	if strings.TrimSpace(explicit) != "" {
		client.OrgID = strings.TrimSpace(explicit)
		return nil
	}
	orgs, err := client.ListOrganizations(cmd.Context())
	if err != nil {
		return err
	}
	if len(orgs) == 0 {
		return fmt.Errorf("you do not belong to any organizations")
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintln(out, "Choose an organization:")
	for i, org := range orgs {
		if strings.TrimSpace(org.ID) == "" {
			return fmt.Errorf("server returned an organization without an ID")
		}
		fmt.Fprintf(out, "  %d. %s (%s)\n", i+1, org.Name, org.ID)
	}
	reader := bufio.NewScanner(cmd.InOrStdin())
	for {
		fmt.Fprint(out, "Organization number or ID (q to cancel): ")
		if !reader.Scan() {
			if err := reader.Err(); err != nil {
				return err
			}
			return fmt.Errorf("organization selection requires input; use --org-id for noninteractive commands")
		}
		choice := strings.TrimSpace(reader.Text())
		if strings.EqualFold(choice, "q") || choice == "" {
			return fmt.Errorf("organization selection canceled")
		}
		for _, org := range orgs {
			if choice == org.ID {
				client.OrgID = org.ID
				return nil
			}
		}
		if index, err := strconv.Atoi(choice); err == nil && index >= 1 && index <= len(orgs) {
			client.OrgID = orgs[index-1].ID
			return nil
		}
		fmt.Fprintln(out, "Select an organization from the list.")
	}
}

func newProjectsCommand(opts *options) *cobra.Command {
	var asJSON bool
	var conversationID string
	list := func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(opts.apiURL)
		if err := selectOrganization(cmd, client, opts.orgID); err != nil {
			return err
		}
		projects, err := client.ListProjects(cmd.Context())
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(projects)
		}
		return printProjects(cmd, projects)
	}
	command := &cobra.Command{Use: "projects", Short: "List or search projects in a selected organization", Args: cobra.NoArgs, RunE: list}
	command.PersistentFlags().BoolVar(&asJSON, "json", false, "Print structured JSON")
	command.AddCommand(&cobra.Command{Use: "list", Short: "List projects in a selected organization", Args: cobra.NoArgs, RunE: list})
	search := &cobra.Command{Use: "search <query>", Short: "Search projects in a selected organization", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			return fmt.Errorf("search query must not be empty")
		}
		client := api.NewClient(opts.apiURL)
		if err := selectOrganization(cmd, client, opts.orgID); err != nil {
			return err
		}
		result, err := client.SearchProjects(cmd.Context(), query, conversationID)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		// Preserve the server's complete search result in JSON mode; human output
		// shows the answer and the matching project summaries.
		if answer, ok := result["answer"].(string); ok && answer != "" {
			fmt.Fprintln(cmd.OutOrStdout(), answer)
		}
		data, err := json.Marshal(result["projects"])
		if err != nil {
			return err
		}
		var projects []api.Project
		if err := json.Unmarshal(data, &projects); err != nil {
			return err
		}
		apps, _ := result["apps"].([]any)
		if len(projects) > 0 || len(apps) == 0 {
			if err := printProjects(cmd, projects); err != nil {
				return err
			}
		}
		if len(apps) > 0 {
			for _, value := range apps {
				if app, ok := value.(map[string]any); ok {
					fmt.Fprintf(cmd.OutOrStdout(), "App: %v (%v) — %v\n", app["name"], app["id"], app["deployment_status"])
				}
			}
		}
		if id, ok := result["conversation_id"].(string); ok && id != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Search conversation: %s\nContinue with --conversation %s\n", id, id)
		}
		return nil
	}}
	search.Flags().StringVar(&conversationID, "conversation", "", "Continue a saved search conversation; omit to start a new search")
	command.AddCommand(search)
	return command
}

func printProjects(cmd *cobra.Command, projects []api.Project) error {
	if len(projects) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No projects found.")
		return err
	}
	out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(out, "ID\tNAME\tDESCRIPTION")
	for _, project := range projects {
		fmt.Fprintf(out, "%s\t%s\t%s\n", project.ID, project.Name, strings.Join(strings.Fields(project.Description), " "))
	}
	return out.Flush()
}

// Graph controls use the server as the source of truth.
func newKnowledgeGraphCommand(opts *options) *cobra.Command {
	var yes, asJSON bool
	cmd := &cobra.Command{Use: "knowledge-graph <status|enable|disable>", Short: "Inspect or enable PK and relationship analysis for this project", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		action := args[0]
		if action != "status" && action != "enable" && action != "disable" {
			return fmt.Errorf("choose status, enable or disable")
		}
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		capacity, err := client.KnowledgeGraphCapacity(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		if asJSON && action == "status" {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(capacity)
		}
		limit := "custom"
		if capacity.Limit != nil {
			limit = strconv.Itoa(*capacity.Limit)
		}
		if !asJSON {
			fmt.Fprintf(cmd.OutOrStdout(), "Knowledge graph enabled: %t\nEligible source datasets: %d\nProject potential pairs: %d\nOrganization capacity if enabled: %d / %s\n", capacity.Enabled, capacity.Datasets, capacity.ProjectPairs, capacity.OrganizationPairs, limit)
		}
		if action == "status" {
			return nil
		}
		enabled := action == "enable"
		if enabled {
			if !capacity.Available {
				return fmt.Errorf("knowledge graph capacity exceeded; disable another project or request a Business quota increase")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Enabling adds %d source datasets (%d potential pairs) to this project's knowledge graph and permits automatic PK and relationship analysis. Transform outputs are excluded.\n", capacity.Datasets, capacity.ProjectPairs)
			if !yes {
				fmt.Fprint(cmd.ErrOrStderr(), "Enable knowledge graph? [y/N]: ")
				scanner := bufio.NewScanner(cmd.InOrStdin())
				if !scanner.Scan() {
					return fmt.Errorf("confirmation required; use --yes for noninteractive enablement")
				}
				answer := strings.TrimSpace(scanner.Text())
				if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
					return fmt.Errorf("knowledge graph enablement canceled")
				}
			}
		}
		if err := client.SetKnowledgeGraph(cmd.Context(), cfg.ProjectID, enabled); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]bool{"enabled": enabled})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Knowledge graph enabled: %t. Existing findings are preserved.\n", enabled)
		return nil
	}}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm enabling after displaying capacity impact")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print structured status")
	return cmd
}
