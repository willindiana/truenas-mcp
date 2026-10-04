package truenas

import (
	"encoding/json"
	"testing"
	"time"
)

func timeoutAfterASecond() <-chan time.Time {
	return time.After(time.Second)
}

// TestSubscriptionKey pins the collection string format that both Subscribe
// and Unsubscribe derive independently, so they cannot drift apart.
func TestSubscriptionKey(t *testing.T) {
	t.Run("nil params yield the bare name", func(t *testing.T) {
		key, err := subscriptionKey("app.container_log_follow", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key != "app.container_log_follow" {
			t.Errorf("got %q", key)
		}
	})

	t.Run("params are appended as JSON", func(t *testing.T) {
		key, err := subscriptionKey("app.container_log_follow", map[string]interface{}{
			"app_name":     "subgen",
			"container_id": "abc123",
			"tail_lines":   10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `app.container_log_follow:{"app_name":"subgen","container_id":"abc123","tail_lines":10}`
		if key != want {
			t.Errorf("got  %s\nwant %s", key, want)
		}
	})

	t.Run("is deterministic across calls", func(t *testing.T) {
		params := map[string]interface{}{"app_name": "immich", "tail_lines": 5}
		first, _ := subscriptionKey("app.container_log_follow", params)
		second, _ := subscriptionKey("app.container_log_follow", params)
		if first != second {
			t.Errorf("keys differ: %q vs %q", first, second)
		}
	})
}

// TestDispatchEventRoutesByExactCollection covers the fast path.
func TestDispatchEventRoutesByExactCollection(t *testing.T) {
	c := &Client{subs: make(map[string]chan Event)}
	key := `app.container_log_follow:{"app_name":"subgen"}`
	ch := make(chan Event, 4)
	c.subs[key] = ch

	resp := APIResponse{
		Msg:        "added",
		Collection: key,
		Fields:     json.RawMessage(`{"data":"line\n","timestamp":"t"}`),
	}

	if !c.dispatchEvent(resp) {
		t.Fatal("dispatchEvent returned false, want true")
	}

	select {
	case ev := <-ch:
		if ev.Msg != "added" {
			t.Errorf("Msg = %q", ev.Msg)
		}
		if ev.Collection != key {
			t.Errorf("Collection = %q", ev.Collection)
		}
		if string(ev.Fields) == "" {
			t.Error("Fields not carried through")
		}
	default:
		t.Fatal("no event delivered")
	}
}

// TestDispatchEventFallsBackToNamePrefix covers a server that echoes the
// subscription params in a different order or spacing than we sent.
func TestDispatchEventFallsBackToNamePrefix(t *testing.T) {
	c := &Client{subs: make(map[string]chan Event)}
	ch := make(chan Event, 4)
	c.subs[`app.container_log_follow:{"app_name":"subgen","tail_lines":10}`] = ch

	// Same event source and app, params serialised differently.
	resp := APIResponse{
		Msg:        "added",
		Collection: `app.container_log_follow:{"tail_lines": 10, "app_name": "subgen"}`,
		Fields:     json.RawMessage(`{"data":"line\n"}`),
	}

	if !c.dispatchEvent(resp) {
		t.Fatal("dispatchEvent returned false, want prefix fallback to match")
	}

	select {
	case <-ch:
	default:
		t.Fatal("prefix fallback did not deliver the event")
	}
}

// TestDispatchEventNoSubscribers ensures normal responses are never swallowed
// by event routing, which would hang every concurrent request on the socket.
func TestDispatchEventNoSubscribers(t *testing.T) {
	c := &Client{subs: make(map[string]chan Event)}

	if c.dispatchEvent(APIResponse{Msg: "result", ID: "7"}) {
		t.Error("a request/response frame must not be treated as an event")
	}
}

// TestDispatchEventDoesNotBlock guards the read loop: a subscriber that stops
// reading must not stall delivery for everyone else.
func TestDispatchEventDoesNotBlock(t *testing.T) {
	c := &Client{subs: make(map[string]chan Event)}
	key := "app.container_log_follow"
	ch := make(chan Event) // unbuffered, nobody reading
	c.subs[key] = ch

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.dispatchEvent(APIResponse{Msg: "added", Collection: key})
	}()

	select {
	case <-done:
	case <-timeoutAfterASecond():
		t.Fatal("dispatchEvent blocked on an unread subscriber")
	}
}

// TestCloseAllSubs ensures channels are closed so collectors can exit.
func TestCloseAllSubs(t *testing.T) {
	c := &Client{subs: make(map[string]chan Event)}
	ch := make(chan Event, 1)
	c.subs["app.container_log_follow"] = ch

	c.closeAllSubs()

	if _, open := <-ch; open {
		t.Error("channel should be closed")
	}
	if len(c.subs) != 0 {
		t.Errorf("subs not cleared: %d left", len(c.subs))
	}
}
