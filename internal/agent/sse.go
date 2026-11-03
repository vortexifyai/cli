package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

type Event struct {
	ID   string
	Type string
	Data []byte
}

func ReadEvents(reader io.Reader, handler func(Event) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	event := Event{}
	var data bytes.Buffer
	hasData := false
	dispatch := func() error {
		if !hasData {
			event = Event{}
			return nil
		}
		if event.Type == "" {
			event.Type = "message"
		}
		event.Data = append([]byte(nil), bytes.TrimSuffix(data.Bytes(), []byte("\n"))...)
		err := handler(event)
		event = Event{}
		data.Reset()
		hasData = false
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			event.ID = value
		case "event":
			event.Type = value
		case "data":
			if data.Len()+len(value) > 4<<20 {
				return fmt.Errorf("SSE event exceeds size limit")
			}
			data.WriteString(value)
			data.WriteByte('\n')
			hasData = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return dispatch()
}
