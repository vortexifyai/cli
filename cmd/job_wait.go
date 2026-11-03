package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"os"
	"strings"
	"sync"
	"time"
	"vortexify-cli/internal/agent"
	"vortexify-cli/internal/api"
)

var jobStreamDone = errors.New("job stream complete")

func waitForJob(ctx context.Context, client *api.Client, projectID, id string, timeout time.Duration, updates ...func(api.Execution)) (api.Execution, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	notify := func(result api.Execution) {
		for _, update := range updates {
			update(result)
		}
	}
	result, err := client.GetJobResults(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	notify(result)
	if result["completed"] == true {
		return result, nil
	}
	after := "0-0"
	failures := 0
	for ctx.Err() == nil {
		if failures < 3 {
			response, streamErr := client.StreamJobStatus(ctx, projectID, id, after)
			if streamErr == nil {
				if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
					streamErr = fmt.Errorf("expected job event stream")
				} else {
					streamErr = agent.ReadEvents(response.Body, func(event agent.Event) error {
						if event.ID != "" {
							after = event.ID
						}
						if event.Type == "error" {
							return fmt.Errorf("job stream interrupted")
						}
						if event.Type != "progress" && event.Type != "done" {
							return nil
						}
						var snapshot api.Execution
						if err := json.Unmarshal(event.Data, &snapshot); err != nil {
							return err
						}
						notify(snapshot)
						if event.Type == "done" || snapshot["completed"] == true {
							return jobStreamDone
						}
						return nil
					})
				}
				response.Body.Close()
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var apiErr *api.APIError
			if errors.As(streamErr, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
				return nil, streamErr
			}
			// Always verify completion in PostgreSQL; EOF and a done event alone are not success.
			result, err = client.GetJobResults(ctx, projectID, id)
			if err != nil {
				return nil, err
			}
			notify(result)
			if result["completed"] == true {
				return result, nil
			}
			failures++
		} else {
			result, err = client.GetJobResults(ctx, projectID, id)
			if err != nil {
				return nil, err
			}
			notify(result)
			if result["completed"] == true {
				return result, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, ctx.Err()
}

func waitForJobDisplay(cmd *cobra.Command, client *api.Client, projectID, id string, timeout time.Duration, asJSON bool) (result api.Execution, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("job %s: %w (waiting stopped; the job was not cancelled)", id, err)
		}
	}()
	if asJSON {
		return waitForJob(cmd.Context(), client, projectID, id, timeout)
	}
	out := cmd.OutOrStdout()
	tty := false
	if file, ok := out.(*os.File); ok {
		tty = term.IsTerminal(int(file.Fd())) && os.Getenv("TERM") != "dumb"
	}
	var mu sync.Mutex
	line, previous := "Waiting for job", ""
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	fmt.Fprintf(out, "Job: %s\n", id)
	if tty {
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			index := 0
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					mu.Lock()
					fmt.Fprintf(out, "\r\x1b[2K%s %s", frames[index%len(frames)], line)
					mu.Unlock()
					index++
				}
			}
		}()
	} else {
		close(stopped)
	}
	result, err = waitForJob(cmd.Context(), client, projectID, id, timeout, func(snapshot api.Execution) {
		mu.Lock()
		defer mu.Unlock()
		stage := textValue(snapshot["stage"])
		if stage == "" {
			stage = textValue(snapshot["status"])
		}
		line = humanLabel(stage)
		if name := textValue(snapshot["resource_name"]); name != "" {
			line += " · " + name
		}
		if reason := textValue(snapshot["waiting_reason"]); reason != "" {
			line += " — " + reason
		}
		for _, pair := range objectList(snapshot["relationships"]) {
			if pair["status"] == "running" {
				line += " · " + textValue(pair["table_a_name"]) + " ↔ " + textValue(pair["table_b_name"])
			}
		}
		if !tty && line != previous {
			fmt.Fprintln(out, "  → "+line)
			previous = line
		}
	})
	close(stop)
	<-stopped
	if tty {
		fmt.Fprint(out, "\r\x1b[2K")
	}
	return result, err
}
