package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// TableSummary contains table metadata and discovery state, never job configuration.
type TableSummary struct {
	ResourceReference string               `json:"resource_reference,omitempty"`
	DisplayName       string               `json:"display_name,omitempty"`
	Description       string               `json:"description,omitempty"`
	KnowledgeGraph    *TableKnowledgeGraph `json:"knowledge_graph,omitempty"`
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Type              string               `json:"type"`
	Environment       string               `json:"environment,omitempty"`
	Status            string               `json:"status"`
	DiscoveryJobID    string               `json:"discovery_job_id,omitempty"`
	Error             string               `json:"error,omitempty"`
	WaitingReason     string               `json:"waiting_reason,omitempty"`
}

// TableKnowledgeGraph is the existing server's table-level discovery summary.
type TableKnowledgeGraph struct {
	TableID            string         `json:"table_id"`
	Status             string         `json:"status"`
	Phase              string         `json:"phase"`
	JobID              string         `json:"job_id,omitempty"`
	KeysComplete       bool           `json:"keys_complete"`
	Relationships      map[string]int `json:"relationships"`
	WaitingForTableIDs []string       `json:"waiting_for_table_ids"`
	PendingJobIDs      []string       `json:"pending_job_ids"`
	Error              string         `json:"error,omitempty"`
	StartedAt          string         `json:"started_at,omitempty"`
	CompletedAt        string         `json:"completed_at,omitempty"`
}

// Flask serializes datetime objects as HTTP dates; other endpoints use ISO dates.
type tableJobTime struct{ time.Time }

func (t *tableJobTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		t.Time = time.Time{}
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == "" {
		t.Time = time.Time{}
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123, time.RFC1123Z} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			t.Time = parsed
			return nil
		}
	}
	return fmt.Errorf("unrecognized job creation timestamp %q", value)
}

type tableDiscoveryJob struct {
	ID         string       `json:"id"`
	ResourceID string       `json:"resource_id"`
	Status     string       `json:"status"`
	CreatedAt  tableJobTime `json:"created_at"`
	Config     struct {
		Preview            bool   `json:"preview"`
		ProjectPublication bool   `json:"project_table_publication"`
		Complete           bool   `json:"watcher_complete"`
		Superseded         bool   `json:"watcher_superseded"`
		Error              string `json:"watcher_error"`
		WaitingReason      string `json:"watcher_wait_reason"`
	} `json:"config"`
}

func (c *Client) ListTables(ctx context.Context, projectID string) ([]TableSummary, error) {
	response, err := c.Service(ctx, "data/tables/list_tables", projectID, map[string]any{})
	if err != nil {
		return nil, err
	}
	tables, err := decode[[]TableSummary](response)
	if err != nil {
		return nil, err
	}
	if len(*tables) == 0 {
		return []TableSummary{}, nil
	}
	// Current servers provide publication/discovery state and qualified labels.
	authoritative := true
	for _, table := range *tables {
		if table.Status == "" {
			authoritative = false
		}
	}
	if authoritative {
		result := []TableSummary{}
		for _, table := range *tables {
			if table.Type == "database" && table.Environment != "origin" {
				continue
			}
			result = append(result, table)
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].DisplayName == result[j].DisplayName {
				return result[i].ID < result[j].ID
			}
			return result[i].DisplayName < result[j].DisplayName
		})
		return result, nil
	}
	jobs := []tableDiscoveryJob{}
	for _, table := range *tables {
		if table.Type == "database" {
			continue
		}
		response, err = c.Service(ctx, "execution/jobs/list_jobs", projectID, map[string]any{
			"type": "schema-discovery", "resource_type": "table", "resource_id": table.ID,
		})
		if err != nil {
			return nil, err
		}
		found, err := decode[[]tableDiscoveryJob](response)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, (*found)...)
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt.Time) })
	latest := map[string]tableDiscoveryJob{}
	published := map[string]bool{}
	for _, job := range jobs {
		if job.Config.Superseded {
			continue
		}
		if _, found := latest[job.ResourceID]; !found {
			latest[job.ResourceID] = job
		}
		if job.Status == "success" && job.Config.Complete && (!job.Config.Preview || job.Config.ProjectPublication) {
			published[job.ResourceID] = true
		}
	}
	result := []TableSummary{}
	for _, table := range *tables {
		if table.Type == "database" {
			if table.Environment != "origin" {
				continue
			}
			table.Status = "registered"
		} else {
			table.Status = "registered; publication unknown"
			if job, found := latest[table.ID]; found {
				table.DiscoveryJobID, table.Error, table.WaitingReason = job.ID, job.Config.Error, job.Config.WaitingReason
				switch job.Status {
				case "created", "pending", "queued":
					table.Status = "schema pending"
				case "running":
					table.Status = "discovering schema"
					if job.Config.Error != "" {
						table.Status = "schema retrying"
					}
				case "success":
					table.Status = "publishing"
					if job.Config.Complete && (!job.Config.Preview || job.Config.ProjectPublication) {
						table.Status = "ready"
					}
				case "failed":
					table.Status = "schema failed"
				case "stopped":
					table.Status = "schema stopped"
				default:
					table.Status = job.Status
				}
				if published[table.ID] && table.Status != "ready" {
					table.Status = "ready; " + table.Status
				}
			}
		}
		result = append(result, table)
	}
	response, err = c.Service(ctx, "platform/projects/get_project_knowledge_graph_status", projectID, map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("get knowledge graph status: %w", err)
	}
	graph, err := decode[struct {
		Tables []TableKnowledgeGraph `json:"tables"`
	}](response)
	if err != nil {
		return nil, err
	}
	byID := map[string]TableKnowledgeGraph{}
	for _, table := range graph.Tables {
		byID[table.TableID] = table
	}
	for i := range result {
		if result[i].KnowledgeGraph != nil {
			continue
		}
		summary, ok := byID[result[i].ID]
		if !ok {
			summary = TableKnowledgeGraph{TableID: result[i].ID, Status: "not_started", Phase: "not_started"}
		}
		result[i].KnowledgeGraph = &summary
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (c *Client) DeleteUploadedTable(ctx context.Context, projectID, tableID string) error {
	response, err := c.Service(ctx, "artifacts/resources/delete_resource", projectID, map[string]any{
		"resource_type": "table", "resource_id": tableID,
	})
	if err != nil {
		return err
	}
	result, err := decode[struct {
		Deleted bool `json:"deleted"`
	}](response)
	if err != nil {
		return err
	}
	if !result.Deleted {
		return fmt.Errorf("server did not confirm table deletion")
	}
	return nil
}

type TableKey struct {
	Columns          []string `json:"columns"`
	UniquePercentage float64  `json:"unique_percentage"`
	GrainDescription string   `json:"grain_description,omitempty"`
	DuplicatesReason string   `json:"duplicates_reason,omitempty"`
}
type TableKeyAnalysis struct {
	Snapshot         string     `json:"analyzed_sync_version,omitempty"`
	Keys             []TableKey `json:"keys"`
	Completed        bool       `json:"completed"`
	GrainDescription string     `json:"grain_description,omitempty"`
}
type TableRelationship struct {
	SourceTableID    string   `json:"source_table_id"`
	TargetTableID    string   `json:"target_table_id"`
	SourceColumns    []string `json:"source_columns"`
	TargetColumns    []string `json:"target_columns"`
	Cardinality      string   `json:"cardinality"`
	RelationshipType string   `json:"relationship_type,omitempty"`
	Description      string   `json:"description,omitempty"`
	MatchingLogic    string   `json:"matching_logic,omitempty"`
}
type TableRelationshipEvaluation struct {
	References    map[string]map[string]any `json:"references,omitempty"`
	OtherTableID  string                    `json:"other_table_id"`
	Status        string                    `json:"status"`
	Outcome       string                    `json:"outcome,omitempty"`
	Reason        string                    `json:"reason,omitempty"`
	LastError     string                    `json:"last_error,omitempty"`
	Relationships []TableRelationship       `json:"relationships"`
}
type TableDetails struct {
	Publication             map[string]any                `json:"publication,omitempty"`
	Table                   TableSummary                  `json:"table"`
	Schema                  map[string]any                `json:"schema,omitempty"`
	KeyAnalysis             *TableKeyAnalysis             `json:"key_analysis"`
	RelationshipEvaluations []TableRelationshipEvaluation `json:"relationship_evaluations"`
}

func (c *Client) GetTableDetails(ctx context.Context, projectID, tableID string, selectors ...map[string]any) (*TableDetails, error) {
	fields := map[string]any{"table_id": tableID, "include_analysis": true}
	for _, selector := range selectors {
		for key, value := range selector {
			fields[key] = value
		}
	}
	response, err := c.Service(ctx, "data/tables/get_table", projectID, fields)
	if err != nil {
		return nil, err
	}
	return decode[TableDetails](response)
}
