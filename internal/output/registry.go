package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"vortexify-cli/internal/resources"

	"vortexify-cli/internal/api"
)

func PrintRegistry(w io.Writer, resources []api.Resource) error {
	t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(t, "TYPE\tNAME\tID\tVERSION\tDEPLOYMENT\tHEALTH\tPLAN\tIMAGE\tENVIRONMENT")
	for _, r := range resources {
		image, environment := "—", "—"
		if r.Environment != nil {
			environment = r.Environment.Status
			if r.Environment.Image != "" {
				image = r.Environment.Image
			}
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n", r.Type, r.Name, r.ID, r.Version, r.DeploymentStatus, r.HealthStatus, r.PlanStatus, image, environment)
	}
	return t.Flush()
}
func PrintResource(w io.Writer, r api.Resource) error { return PrintRegistry(w, []api.Resource{r}) }

type ResourceStatus struct {
	Files                          *resources.FileStatus `json:"files,omitempty"`
	Name, Registration, Change     string
	Preview                        string `json:"-"`
	Deployment                     string `json:"-"`
	Upstream, Downstream, Problems []string
	UpstreamTables                 []string `json:"-"`
}

func PrintStatus(w io.Writer, rows []ResourceStatus) error {
	for i, row := range rows {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		source := fmt.Sprintf("Source: %s, %s", row.Registration, row.Change)
		if row.Files != nil {
			source = "Code: " + row.Change
		}
		if _, err := fmt.Fprintf(w, "%s\n  %s\n", row.Name, source); err != nil {
			return err
		}
		if row.Registration == "not registered" {
			kind, name, _ := strings.Cut(row.Name, ":")
			if _, err := fmt.Fprintf(w, "  Next: vortexify create %s %s (register), or vortexify commit (register and upload)\n", kind, name); err != nil {
				return err
			}
		}
		if row.Files != nil {
			parts := []string{}
			if len(row.Files.Added) > 0 {
				parts = append(parts, fmt.Sprintf("%d added", len(row.Files.Added)))
			}
			if len(row.Files.Removed) > 0 {
				parts = append(parts, fmt.Sprintf("%d removed", len(row.Files.Removed)))
			}
			summary := fmt.Sprintf("%d attached — clean", row.Files.Total)
			if row.Files.Changed {
				summary = strings.Join(parts, ", ") + " — uncommitted"
			}
			if _, err := fmt.Fprintf(w, "  Attachments: %s\n", summary); err != nil {
				return err
			}
			for _, file := range row.Files.Added {
				if _, err := fmt.Fprintf(w, "    + %s\n", file.Name); err != nil {
					return err
				}
			}
			for _, file := range row.Files.Removed {
				if _, err := fmt.Fprintf(w, "    - %s\n", file.Name); err != nil {
					return err
				}
			}
		}
		if row.Preview != "" {
			if _, err := fmt.Fprintf(w, "  Preview: %s\n", row.Preview); err != nil {
				return err
			}
		}
		if row.Deployment != "" {
			if _, err := fmt.Fprintf(w, "  Deployed: %s\n", row.Deployment); err != nil {
				return err
			}
		}
		if len(row.UpstreamTables) > 0 {
			if _, err := fmt.Fprintln(w, "  Upstream tables:"); err != nil {
				return err
			}
			for _, item := range row.UpstreamTables {
				if _, err := fmt.Fprintf(w, "    %s\n", item); err != nil {
					return err
				}
			}
		}
		for _, item := range row.Upstream {
			if _, err := fmt.Fprintf(w, "  Upstream: %s\n", item); err != nil {
				return err
			}
		}
		for _, item := range row.Downstream {
			if _, err := fmt.Fprintf(w, "  Downstream: %s%s\n", item, impactLabel(row.Change)); err != nil {
				return err
			}
		}
		for _, item := range row.Problems {
			if _, err := fmt.Fprintf(w, "  Error: %s\n", item); err != nil {
				return err
			}
		}
	}
	return nil
}

func impactLabel(change string) string {
	switch change {
	case "new", "modified", "untracked local code", "diverged; pull to reconcile":
		return " (potentially affected)"
	default:
		return ""
	}
}
