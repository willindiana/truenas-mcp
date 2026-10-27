package truenas

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAppLifecycleLogs(t *testing.T) {
	for _, mode := range []string{"ok", "external", "network-path", "traversal", "encoded-traversal", "redirect", "oversize", "malformed", "bad-job", "api-error"} {
		t.Run(mode, func(t *testing.T) {
			downloads := 0
			client := newDownloadFake(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/_download/") {
					t.Errorf("unexpected download path: %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				downloads++
				if r.URL.Query().Get("auth_token") != "secret-token" {
					t.Error("missing token")
				}
				switch mode {
				case "redirect":
					http.Redirect(w, r, "/redirected?auth_token=secret-token", http.StatusFound)
				case "oversize":
					fmt.Fprint(w, strings.Repeat("x", (16<<20)+1))
				default:
					fmt.Fprint(w, "first\nlast\n")
				}
			}, func(req downloadRequest) (interface{}, map[string]interface{}) {
				if req.Method != "core.download" {
					return true, nil
				}
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
				case "malformed":
					return path, nil
				case "bad-job":
					return []interface{}{"42", path}, nil
				case "api-error":
					return nil, downloadRPCError("secret-token")
				}
				return []interface{}{42, path}, nil
			})
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
