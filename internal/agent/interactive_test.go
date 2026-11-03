package agent

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestInteractCancelsWhileWaitingForInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{Output: io.Discard}
	done := make(chan error, 1)
	go func() { done <- session.Interact(ctx, reader) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle agent did not cancel")
	}
}
