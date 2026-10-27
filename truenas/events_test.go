package truenas

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const logFollow = "app.container_log_follow"

func notification(t *testing.T, method string, params map[string]interface{}) *rpcResponse {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return &rpcResponse{JSONRPC: "2.0", Method: method, Params: raw}
}

func clientWithSub(key string, conn *websocket.Conn, size int) (*Client, *subscription) {
	sub := &subscription{conn: conn, ch: make(chan Event, size)}
	return &Client{subs: map[string]*subscription{key: sub}}, sub
}

func TestSubscriptionKey(t *testing.T) {
	if key, _ := subscriptionKey(logFollow, nil); key != logFollow {
		t.Errorf("nil params: got %q", key)
	}
	key, err := subscriptionKey(logFollow, map[string]interface{}{"tail_lines": 10, "container_id": "abc123", "app_name": "subgen"})
	if err != nil {
		t.Fatal(err)
	}
	want := logFollow + `:{"app_name":"subgen","container_id":"abc123","tail_lines":10}`
	if key != want {
		t.Errorf("got  %s\nwant %s", key, want)
	}
}

// A server echo with other key order or spacing still reaches its subscriber.
func TestDispatchCanonicalizesCollection(t *testing.T) {
	key, _ := subscriptionKey(logFollow, map[string]interface{}{"app_name": "subgen", "tail_lines": 10})
	c, sub := clientWithSub(key, nil, 4)
	resp := notification(t, "collection_update", map[string]interface{}{
		"msg": "added", "collection": logFollow + `:{"tail_lines": 10, "app_name": "subgen"}`,
		"fields": map[string]interface{}{"data": "line\n"},
	})
	if !c.dispatchNotification(nil, resp) {
		t.Fatal("event not routed")
	}
	ev := <-sub.ch
	if ev.Msg != "added" || !strings.Contains(string(ev.Fields), "line") {
		t.Errorf("bad event: %+v", ev)
	}
}

// Two apps' log streams must never be cross-delivered on a guess.
func TestDispatchDoesNotGuessBetweenSubscriptions(t *testing.T) {
	a, _ := subscriptionKey(logFollow, map[string]interface{}{"app_name": "a"})
	b, _ := subscriptionKey(logFollow, map[string]interface{}{"app_name": "b"})
	c, subA := clientWithSub(a, nil, 4)
	c.subs[b] = &subscription{ch: make(chan Event, 4)}

	for _, coll := range []string{logFollow + `:{"app_name":"other"}`, logFollow} {
		resp := notification(t, "collection_update", map[string]interface{}{"msg": "added", "collection": coll})
		if c.dispatchNotification(nil, resp) {
			t.Errorf("collection %q routed to a subscription it does not name", coll)
		}
	}
	// A bare name with a single matching subscription is unambiguous.
	delete(c.subs, b)
	if !c.dispatchNotification(nil, notification(t, "collection_update", map[string]interface{}{"msg": "added", "collection": logFollow})) {
		t.Error("bare name with one subscription not routed")
	}
	if len(subA.ch) != 1 {
		t.Errorf("subscriber got %d events, want 1", len(subA.ch))
	}
}

// Events for another connection's subscription, and unrelated notifications,
// are not consumed.
func TestDispatchIgnoresForeignFrames(t *testing.T) {
	c, _ := clientWithSub(logFollow, &websocket.Conn{}, 4)
	ev := map[string]interface{}{"msg": "added", "collection": logFollow}
	if c.dispatchNotification(&websocket.Conn{}, notification(t, "collection_update", ev)) {
		t.Error("event routed across connections")
	}
	if c.dispatchNotification(c.subs[logFollow].conn, notification(t, "some_other_method", ev)) {
		t.Error("non-subscription notification consumed")
	}
}

func TestDispatchDoesNotBlock(t *testing.T) {
	c, _ := clientWithSub(logFollow, nil, 0) // unbuffered, nobody reading
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.dispatchNotification(nil, notification(t, "collection_update", map[string]interface{}{"msg": "added", "collection": logFollow}))
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatch blocked on an unread subscriber")
	}
}

func TestNotifyUnsubscribedClosesChannel(t *testing.T) {
	c, sub := clientWithSub(logFollow, nil, 1)
	c.dispatchNotification(nil, notification(t, "notify_unsubscribed", map[string]interface{}{"collection": logFollow}))
	if _, open := <-sub.ch; open {
		t.Error("channel should be closed")
	}
	if len(c.subs) != 0 {
		t.Error("subscription not removed")
	}
	if err := c.Unsubscribe(logFollow, nil); err != nil { // already ended: no-op, no double close
		t.Errorf("Unsubscribe after server end: %v", err)
	}
}

func TestCloseSubsOnlyForThatConn(t *testing.T) {
	dead, live := &websocket.Conn{}, &websocket.Conn{}
	c, sub := clientWithSub("a", dead, 1)
	other := &subscription{conn: live, ch: make(chan Event, 1)}
	c.subs["b"] = other
	c.closeSubs(dead)
	if _, open := <-sub.ch; open {
		t.Error("dead connection's channel should be closed")
	}
	if c.subs["b"] != other {
		t.Error("live connection's subscription removed")
	}
}

// subFake is a minimal JSON-RPC server for core.subscribe: it logs in,
// answers core.subscribe with "sub-1" followed by two events, and records
// core.unsubscribe params.
type subFake struct {
	*httptest.Server
	mu          sync.Mutex
	unsubscribe []string
	conn        *websocket.Conn
}

func newSubFake(t *testing.T) *subFake {
	f := &subFake{}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		f.mu.Lock()
		f.conn = conn
		f.mu.Unlock()
		for {
			var req struct {
				ID     json.RawMessage   `json:"id"`
				Method string            `json:"method"`
				Params []json.RawMessage `json:"params"`
			}
			if conn.ReadJSON(&req) != nil {
				return
			}
			reply := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": nil}
			switch req.Method {
			case "auth.login_ex":
				reply["result"] = map[string]interface{}{"response_type": "SUCCESS"}
			case "core.subscribe":
				reply["result"] = "sub-1"
			case "core.unsubscribe":
				f.mu.Lock()
				f.unsubscribe = append(f.unsubscribe, string(req.Params[0]))
				f.mu.Unlock()
			}
			conn.WriteJSON(reply)
			if req.Method == "core.subscribe" {
				var name string
				json.Unmarshal(req.Params[0], &name)
				for _, line := range []string{"one", "two"} {
					conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "method": "collection_update",
						"params": map[string]interface{}{"msg": "added", "collection": name, "fields": map[string]interface{}{"data": line}}})
				}
			}
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func TestSubscribeRoundTrip(t *testing.T) {
	f := newSubFake(t)
	c, err := NewClientWithOptions("wss"+strings.TrimPrefix(f.URL, "https")+apiPath, testAPIKey, &tls.Config{InsecureSkipVerify: true}, Options{APIUser: testAPIUser})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	params := map[string]interface{}{"app_name": "subgen", "container_id": "abc", "tail_lines": 2}

	events, err := c.Subscribe(logFollow, params)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := c.Subscribe(logFollow, params); err == nil {
		t.Error("duplicate Subscribe should fail, not hand out a dead channel")
	}
	for _, want := range []string{"one", "two"} {
		select {
		case ev := <-events:
			if !strings.Contains(string(ev.Fields), want) {
				t.Errorf("got %s, want %s", ev.Fields, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("event not delivered")
		}
	}
	if err := c.Unsubscribe(logFollow, params); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if _, open := <-events; open {
		t.Error("channel open after Unsubscribe")
	}
	f.mu.Lock()
	got := f.unsubscribe
	f.mu.Unlock()
	if len(got) != 1 || got[0] != `"sub-1"` {
		t.Errorf("core.unsubscribe params = %v, want the subscription id", got)
	}

	// A dropped connection closes a live subscription's channel.
	events, err = c.Subscribe(logFollow, params)
	if err != nil {
		t.Fatalf("re-Subscribe: %v", err)
	}
	f.mu.Lock()
	f.conn.Close()
	f.mu.Unlock()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after connection drop")
		}
	}
}
