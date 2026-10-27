package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/truenas/truenas-mcp/truenas"
)

// TestDecodeLogData covers the two payload shapes an app.container_log_follow
// event can arrive in, plus the empty case.
func TestDecodeLogData(t *testing.T) {
	tests := []struct {
		name string
		ev   truenas.Event
		want string
	}{
		{
			name: "fields.data is the log line",
			ev: truenas.Event{
				Fields: json.RawMessage(`{"data":"hello from container\n","timestamp":"2026-10-04 01:37:57.911774+00:00"}`),
			},
			want: "hello from container",
		},
		{
			name: "falls back to result when fields absent",
			ev: truenas.Event{
				Result: json.RawMessage(`{"data":"from result\n","timestamp":"t"}`),
			},
			want: "from result",
		},
		{
			name: "strips only the trailing newline",
			ev: truenas.Event{
				Fields: json.RawMessage(`{"data":"line one\nline two\n"}`),
			},
			want: "line one\nline two",
		},
		{
			name: "empty data yields empty string",
			ev: truenas.Event{
				Fields: json.RawMessage(`{"data":"","timestamp":"t"}`),
			},
			want: "",
		},
		{
			name: "no payload at all",
			ev:   truenas.Event{},
			want: "",
		},
		{
			name: "malformed payload is ignored",
			ev: truenas.Event{
				Fields: json.RawMessage(`not json`),
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeLogData(tt.ev); got != tt.want {
				t.Errorf("decodeLogData() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFormatLogLine checks the timestamp prefix and the truncation guard.
func TestFormatLogLine(t *testing.T) {
	t.Run("prefixes timestamp", func(t *testing.T) {
		fields := json.RawMessage(`{"data":"boom","timestamp":"2026-09-11 03:49:26.853406+00:00"}`)
		got := formatLogLine("boom", fields)
		if !strings.HasPrefix(got, "2026-09-11 03:49:26.853406+00:00 ") {
			t.Errorf("missing timestamp prefix: %q", got)
		}
		if !strings.HasSuffix(got, "boom") {
			t.Errorf("log text missing: %q", got)
		}
	})

	t.Run("no timestamp yields bare text", func(t *testing.T) {
		if got := formatLogLine("boom", json.RawMessage(`{}`)); got != "boom" {
			t.Errorf("formatLogLine() = %q, want %q", got, "boom")
		}
	})

	t.Run("truncates oversized entries", func(t *testing.T) {
		huge := strings.Repeat("x", logOversizeDataWarn+500)
		got := formatLogLine(huge, json.RawMessage(`{}`))
		if !strings.HasSuffix(got, "... [truncated]") {
			t.Errorf("expected truncation marker, got %d bytes", len(got))
		}
		if len(got) > logOversizeDataWarn+64 {
			t.Errorf("output not bounded: %d bytes", len(got))
		}
	})
}

// TestHandleAppLogsRequiresAppName keeps the handler from calling the API with
// an empty app name.
func TestHandleAppLogsRequiresAppName(t *testing.T) {
	for _, tool := range []func(*truenas.Client, map[string]interface{}) (string, error){
		handleAppLogs,
		handleAppContainers,
	} {
		if _, err := tool(nil, map[string]interface{}{}); err == nil {
			t.Error("expected an error when app_name is missing")
		}
	}
}

func TestHandleAppLogsRejectsInvalidTailBeforeAPI(t *testing.T) {
	for _, v := range []interface{}{0.0, 1.5, float64(maxLogTail + 1), "10"} {
		if _, err := handleAppLogs(nil, map[string]interface{}{"app_name": "x", "tail_lines": v}); err == nil || !strings.Contains(err.Error(), "tail_lines") {
			t.Errorf("tail_lines=%v: err=%v", v, err)
		}
	}
}

func TestSortContainers(t *testing.T) {
	c := []map[string]interface{}{{"service_name": "web", "id": "b"}, {"service_name": "db", "id": "c"}, {"service_name": "web", "id": "a"}}
	sortContainers(c)
	if c[0]["id"] != "c" || c[1]["id"] != "a" || c[2]["id"] != "b" {
		t.Fatalf("unexpected order: %v", c)
	}
}
