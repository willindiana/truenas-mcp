package truenas

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestJobLogs(t *testing.T) {
	for _, mode := range []string{"ok", "external", "redirect", "oversize", "missing"} {
		t.Run(mode, func(t *testing.T) {
			client := newDownloadFake(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/_download/1" {
					t.Errorf("unexpected download path: %s", r.URL.Path)
					return
				}
				if r.URL.Query().Get("auth_token") != "secret-token" {
					t.Error("missing token")
				}
				switch mode {
				case "redirect":
					http.Redirect(w, r, "https://example.com", http.StatusFound)
				case "oversize":
					fmt.Fprint(w, strings.Repeat("x", (16<<20)+1))
				default:
					fmt.Fprint(w, "first\nlast\n")
				}
			}, func(req downloadRequest) (interface{}, map[string]interface{}) {
				if req.Method != "core.job_download_logs" {
					return true, nil
				}
				if len(req.Params) != 3 || req.Params[0] != float64(42) || req.Params[2] != false {
					t.Errorf("unexpected params: %v", req.Params)
				}
				switch mode {
				case "external":
					return "https://example.com/_download/1?auth_token=secret-token", nil
				case "missing":
					return nil, downloadRPCError("This job has no logs")
				}
				return "/_download/1?auth_token=secret-token", nil
			})
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
