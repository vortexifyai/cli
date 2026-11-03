package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

func (s *Session) Interact(ctx context.Context, input io.Reader) error {
	type line struct {
		text string
		err  error
	}
	lines := make(chan line)
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		reader := bufio.NewReader(input)
		for {
			text, err := reader.ReadString('\n')
			select {
			case lines <- line{text, err}:
			case <-readCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		if _, err := fmt.Fprint(s.Output, "> "); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case value := <-lines:
			if value.err != nil && value.err != io.EOF {
				return value.err
			}
			text := strings.TrimSpace(value.text)
			if text == "/exit" || text == "/quit" {
				return nil
			}
			if text != "" {
				if err := s.Send(ctx, text); err != nil {
					return err
				}
			}
			if value.err == io.EOF {
				return nil
			}
		}
	}
}
