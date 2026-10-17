package tools

import (
	"math"
	"strings"
	"testing"
)

func TestAppLifecycleLogValidation(t *testing.T) {
	for _, value := range []interface{}{nil, "200", true, 0.0, -1.0, 1.5, 5001.0, math.NaN(), math.Inf(1)} {
		if _, err := handleAppLifecycleLogs(nil, map[string]interface{}{"tail_lines": value}); err == nil {
			t.Errorf("accepted tail_lines=%v", value)
		}
	}
	for _, key := range []string{"path", "app_name"} {
		if _, err := handleAppLifecycleLogs(nil, map[string]interface{}{key: "anything"}); err == nil {
			t.Errorf("accepted %s", key)
		}
	}
	for _, tc := range []struct {
		args map[string]interface{}
		want int
	}{
		{nil, 200}, {map[string]interface{}{"tail_lines": 1.0}, 1}, {map[string]interface{}{"tail_lines": 5000.0}, 5000},
	} {
		got, err := boundedJobInt(tc.args, "tail_lines", 200, 5000)
		if err != nil || got != tc.want {
			t.Fatalf("tail=%d err=%v", got, err)
		}
	}
}

func TestLogTail(t *testing.T) {
	text, truncated, err := logTail([]byte("first\nsecond\nlast\n"), 2)
	if err != nil || text != "second\nlast" || !truncated {
		t.Fatalf("text=%q truncated=%v err=%v", text, truncated, err)
	}
	text, truncated, err = logTail(nil, 200)
	if err != nil || text != "" || truncated {
		t.Fatalf("empty text=%q truncated=%v err=%v", text, truncated, err)
	}
	if _, _, err := logTail([]byte(strings.Repeat("x", (1<<20)+1)), 1); err == nil {
		t.Fatal("accepted oversized output")
	}
}
