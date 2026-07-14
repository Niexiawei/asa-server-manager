package httpserver

import (
	"testing"
	"time"
)

func TestTaskBroadcasterHistory(t *testing.T) {
	tb := NewTaskBroadcaster()
	if got := tb.GetHistory(); got != nil {
		t.Fatalf("expected nil history before start, got %v", got)
	}

	if !tb.Start() {
		t.Fatal("expected Start to succeed")
	}
	defer tb.Stop()

	tb.SendMessage("line-1")
	tb.SendMessage("line-2")

	history := tb.GetHistory()
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}
	if history[0] != "line-1" || history[1] != "line-2" {
		t.Fatalf("unexpected history: %v", history)
	}

	// GetHistory returns a copy
	history[0] = "mutated"
	if tb.GetHistory()[0] != "line-1" {
		t.Fatal("GetHistory should return a defensive copy")
	}
}

func TestTaskBroadcasterStartClearsHistory(t *testing.T) {
	tb := NewTaskBroadcaster()
	if !tb.Start() {
		t.Fatal("expected first Start to succeed")
	}
	tb.SendMessage("old")
	tb.Stop()

	if !tb.Start() {
		t.Fatal("expected second Start to succeed")
	}
	defer tb.Stop()

	if len(tb.GetHistory()) != 0 {
		t.Fatalf("expected history cleared on Start, got %v", tb.GetHistory())
	}
}

// TestTaskBroadcasterSubscribeBeforeStartSurvives mirrors handleServerUpdate's
// Subscribe-first ordering: a client subscribes before the task is Start()ed. Start()
// must NOT close that fresh subscriber, otherwise the SSE stream ends immediately.
func TestTaskBroadcasterSubscribeBeforeStartSurvives(t *testing.T) {
	tb := NewTaskBroadcaster()

	// Subscribe BEFORE Start (the TOCTOU-safe path).
	ch, unsubscribe := tb.Subscribe()
	defer unsubscribe()

	if !tb.Start() {
		t.Fatal("expected Start to succeed")
	}
	defer tb.Stop()

	tb.SendMessage("live")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg, ok := <-ch:
			if !ok {
				t.Fatal("subscriber channel was closed by Start(); stream would disconnect immediately")
			}
			if msg != "live" {
				t.Fatalf("expected live message, got %q", msg)
			}
			return
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Fatal("expected pre-Start subscriber to receive message")
}

func TestTaskBroadcasterSubscribeReceivesMessages(t *testing.T) {
	tb := NewTaskBroadcaster()
	if !tb.Start() {
		t.Fatal("expected Start to succeed")
	}
	defer tb.Stop()

	ch, unsubscribe := tb.Subscribe()
	defer unsubscribe()

	tb.SendMessage("live")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-ch:
			if msg != "live" {
				t.Fatalf("expected live message, got %q", msg)
			}
			return
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Fatal("expected subscriber to receive message")
}
