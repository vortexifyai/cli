package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestSSEEvents(t *testing.T) {
	var events []Event
	input := ": heartbeat\r\nevent: token\r\ndata: first\r\ndata: second\r\n\r\ndata: default\n\nevent: done\ndata: {}"
	if err := ReadEvents(strings.NewReader(input), func(e Event) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != "token" || string(events[0].Data) != "first\nsecond" || events[1].Type != "message" || events[2].Type != "done" {
		t.Fatalf("events %#v", events)
	}
}
func TestSSEHandlerError(t *testing.T) {
	sentinel := errors.New("stop")
	err := ReadEvents(strings.NewReader("data: hi\n\n"), func(Event) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}
func TestSSESizeLimit(t *testing.T) {
	if err := ReadEvents(strings.NewReader("data: "+strings.Repeat("x", 5<<20)), func(Event) error { return nil }); err == nil {
		t.Fatal("accepted oversized event")
	}
}
