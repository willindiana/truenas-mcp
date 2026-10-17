package truenas

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestJobLogs(t *testing.T) {
	for _, mode := range []string{"ok", "external", "redirect", "oversize", "missing"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_download/1" {
					if r.URL.Query().Get("auth_token") != "secret-token" {
						t.Error("missing token")
					}
					if mode == "redirect" {
						http.Redirect(w, r, "https://example.com", http.StatusFound)
						return
					}
					if mode == "oversize" {
						fmt.Fprint(w, strings.Repeat("x", (16<<20)+1))
						return
					}
					fmt.Fprint(w, "first\nlast\n")
					return
				}
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				var hello interface{}
				if conn.ReadJSON(&hello) != nil {
					return
				}
				conn.WriteJSON(map[string]interface{}{"msg": "connected"})
				for {
					var req APIRequest
					if conn.ReadJSON(&req) != nil {
						return
					}
					var result interface{} = true
					if req.Method == "core.job_download_logs" {
						if len(req.Params) != 3 || req.Params[0] != float64(42) || req.Params[2] != false {
							t.Errorf("unexpected params: %v", req.Params)
						}
						result = "/_download/1?auth_token=secret-token"
						if mode == "external" {
							result = "https://example.com/_download/1?auth_token=secret-token"
						}
						if mode == "missing" {
							conn.WriteJSON(map[string]interface{}{"id": req.ID, "msg": "result", "error": map[string]interface{}{"error": 2, "reason": "This job has no logs"}})
							continue
						}
					}
					conn.WriteJSON(map[string]interface{}{"id": req.ID, "msg": "result", "result": result})
				}
			}))
			defer server.Close()
			client, err := NewClient("wss"+strings.TrimPrefix(server.URL, "https")+"/websocket", testAPIKey, server.Client().Transport.(*http.Transport).TLSClientConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			data, err := client.JobLogs(42)
			if mode == "ok" {
				if err != nil || string(data) != "first\nlast\n" {
					t.Fatalf("data=%q err=%v", data, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
	if got := string(RedactJSONForLog([]byte(`{"result":"/_download/1?auth_token=secret-token"}`))); strings.Contains(got, "secret-token") {
		t.Fatal("download token leaked")
	}
}
