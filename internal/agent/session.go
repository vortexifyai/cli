package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"vortexify-cli/internal/api"
)

type Session struct {
	Client    *api.Client
	ProjectID string
	SessionID string
	Output    io.Writer
}

func NewSession(ctx context.Context, client *api.Client, projectID string, output io.Writer) (*Session, error) {
	return &Session{Client: client, ProjectID: projectID, Output: output}, nil
}

var errDone = errors.New("stream done")

func tokenText(data []byte) string {
	var text string
	if json.Unmarshal(data, &text) == nil {
		return text
	}
	var object struct {
		Text    string `json:"text"`
		Token   string `json:"token"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &object) == nil {
		if object.Text != "" {
			return object.Text
		}
		if object.Token != "" {
			return object.Token
		}
		return object.Message
	}
	return string(data)
}
func (s *Session) Send(ctx context.Context, message string) error {
	run, err := s.Client.QueryManagerAgent(ctx, s.ProjectID, s.SessionID, message)
	if err != nil {
		return err
	}
	s.SessionID = run.ConversationID
	resp, err := s.Client.StreamManagerAgent(ctx, s.ProjectID, run)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("expected text/event-stream response")
	}
	err = ReadEvents(resp.Body, func(event Event) error {
		switch event.Type {
		case "token", "message":
			_, err := fmt.Fprint(s.Output, tokenText(event.Data))
			return err
		case "error":
			return fmt.Errorf("agent: %s", tokenText(event.Data))
		case "done":
			return errDone
		}
		return nil
	})
	if errors.Is(err, errDone) {
		_, err = fmt.Fprintln(s.Output)
		return err
	}
	if err == nil {
		return io.ErrUnexpectedEOF
	}
	return err
}
