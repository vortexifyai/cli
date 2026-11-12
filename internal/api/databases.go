package api

import (
	"context"
	"encoding/json"
)

type PendingDatabaseMigration struct {
	Version        int      `json:"version"`
	Environment    string   `json:"environment"`
	MigrationPaths []string `json:"migration_paths"`
}
type DatabaseMigrationJob struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Version     int    `json:"resource_version"`
	Environment string `json:"environment,omitempty"`

	Error string `json:"error,omitempty"`
}

func (j *DatabaseMigrationJob) UnmarshalJSON(data []byte) error {
	type job DatabaseMigrationJob
	var value struct {
		job
		Config struct {
			Environment string `json:"environment"`
			Error       string `json:"watcher_error"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*j = DatabaseMigrationJob(value.job)
	if j.Environment == "" {
		j.Environment = value.Config.Environment
	}
	if j.Error == "" {
		j.Error = value.Config.Error
	}
	return nil
}

type DatabaseBackupJob struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Environment string `json:"environment"`
	Version     int    `json:"resource_version"`
}
type DatabaseEnvironment struct {
	Environment string `json:"environment"`
	Version     int    `json:"version"`
}
type DatabaseStatus struct {
	Environments     []DatabaseEnvironment      `json:"environments"`
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Version          int                        `json:"version"`
	ApprovalRequired bool                       `json:"migration_approval_required"`
	Pending          []PendingDatabaseMigration `json:"pending_migrations"`
	LatestMigrations []DatabaseMigrationJob     `json:"latest_migrations"`
	LatestMigration  *DatabaseMigrationJob      `json:"latest_migration,omitempty"`
	LatestBackups    []DatabaseBackupJob        `json:"latest_backups"`
}
type DatabaseDecision struct {
	Version     int    `json:"version"`
	Environment string `json:"environment"`
	Approved    bool   `json:"approved"`
	// Already-applied versions return table rows instead of a new job.
	MigrationResult any `json:"migration_result,omitempty"`
}

func (c *Client) GetDatabaseStatus(ctx context.Context, projectID, databaseID string) (*DatabaseStatus, error) {
	response, err := c.Service(ctx, "artifacts/resources/get_resource", projectID, map[string]any{
		"resource_type": "database", "resource_id": databaseID,
	})
	if err != nil {
		return nil, err
	}
	status, err := decode[DatabaseStatus](response)
	if err != nil {
		return nil, err
	}
	response, err = c.Service(ctx, "data/databases/get_pending_database_migrations", projectID, map[string]any{"database_id": databaseID})
	if err != nil {
		return nil, err
	}
	pending, err := decode[[]PendingDatabaseMigration](response)
	if err != nil {
		return nil, err
	}
	status.Pending = *pending
	response, err = c.Service(ctx, "execution/jobs/list_jobs", projectID, map[string]any{
		"resource_type": "database", "resource_id": databaseID, "type": "database-migrate",
	})
	if err != nil {
		return nil, err
	}
	jobs, err := decode[[]DatabaseMigrationJob](response)
	if err != nil {
		return nil, err
	}
	// Keep the newest attempt per environment; another environment cannot hide it.
	seen := map[string]bool{}
	for _, job := range *jobs {
		if !seen[job.Environment] {
			status.LatestMigrations = append(status.LatestMigrations, job)
			seen[job.Environment] = true
		}
	}
	// The existing jobs endpoint returns creation time descending.
	if len(*jobs) > 0 {
		status.LatestMigration = &(*jobs)[0]
	}
	response, err = c.Service(ctx, "data/databases/get_database_status", projectID, map[string]any{"database_id": databaseID})
	if err != nil {
		return nil, err
	}
	operations, err := decode[struct {
		LatestBackupJobs []DatabaseBackupJob   `json:"latest_backup_jobs"`
		Environments     []DatabaseEnvironment `json:"environments"`
	}](response)
	if err != nil {
		return nil, err
	}
	status.LatestBackups = operations.LatestBackupJobs
	status.Environments = operations.Environments
	return status, nil
}

func (c *Client) DecideDatabaseMigration(ctx context.Context, projectID, databaseID string, version int, environment string, approve bool) (*DatabaseDecision, error) {
	action := "reject_database_migration"
	if approve {
		action = "approve_database_migration"
	}
	response, err := c.Service(ctx, "data/databases/"+action, projectID, map[string]any{
		"database_id": databaseID, "version": version, "environment": environment,
	})
	if err != nil {
		return nil, err
	}
	return decode[DatabaseDecision](response)
}

func (c *Client) MigrateDatabase(ctx context.Context, projectID, databaseID string, version int, environment string) (any, error) {
	response, err := c.Service(ctx, "data/databases/migrate_schema", projectID, map[string]any{
		"database_id": databaseID, "version": version, "environment": environment,
	})
	if err != nil {
		return nil, err
	}
	result, err := decode[any](response)
	if err != nil {
		return nil, err
	}
	return *result, nil
}
