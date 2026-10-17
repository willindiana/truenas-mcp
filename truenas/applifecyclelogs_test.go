package truenas

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestAppLifecycleLogs(t *testing.T) {
	for _, mode := range []string{"ok", "external", "network-path", "traversal", "encoded-traversal", "redirect", "oversize", "malformed", "bad-job", "api-error"} {
		t.Run(mode, func(t *testing.T) {
			downloads := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/_download/") {
					downloads++
					if r.URL.Query().Get("auth_token") != "secret-token" {
						t.Error("missing token")
					}
					if mode == "redirect" {
						http.Redirect(w, r, "/redirected?auth_token=secret-token", http.StatusFound)
						return
					}
					if mode == "oversize" {
						fmt.Fprint(w, strings.Repeat("x", (16<<20)+1))
						return
					}
					fmt.Fprint(w, "first\nlast\n")
					return
				}
				if r.URL.Path != "/websocket" {
					t.Errorf("unexpected download path: %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
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
					if req.Method == "core.download" {
						if fmt.Sprint(req.Params) != "[filesystem.get [/var/log/app_lifecycle.log] app_lifecycle.log false]" {
							t.Errorf("unexpected params: %v", req.Params)
						}
						path := "/_download/1?auth_token=secret-token"
						switch mode {
						case "external":
							path = "https://example.com" + path
						case "network-path":
							path = "//example.com" + path
						case "traversal":
							path = "/_download/../private?auth_token=secret-token"
						case "encoded-traversal":
							path = "/_download/%2e%2e/private?auth_token=secret-token"
						}
						result = []interface{}{42, path}
						if mode == "malformed" {
							result = path
						}
						if mode == "bad-job" {
							result = []interface{}{"42", path}
						}
						if mode == "api-error" {
							conn.WriteJSON(map[string]interface{}{"id": req.ID, "msg": "result", "error": map[string]interface{}{"error": 13, "reason": "secret-token"}})
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
			data, err := client.AppLifecycleLogs()
			if mode == "ok" {
				if err != nil || string(data) != "first\nlast\n" || downloads != 1 {
					t.Fatalf("data=%q err=%v downloads=%d", data, err, downloads)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
}
