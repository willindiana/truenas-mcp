package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStartupWithoutBackend(t *testing.T) {
	if os.Getenv("TRUENAS_MCP_STARTUP_TEST") == "1" {
		os.Args = os.Args[:1]
		*truenasURL = "wss://127.0.0.1:1/websocket"
		*apiKey = "test-only"
		main()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupWithoutBackend$")
	cmd.Env = append(os.Environ(), "TRUENAS_MCP_STARTUP_TEST=1", "TRUENAS_TLS_CA=", "TRUENAS_MCP_DEBUG=0")
	cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("startup with unavailable backend: %v: %s", err, &stderr)
	}
	decoder := json.NewDecoder(&stdout)
	for _, id := range []int{1, 2} {
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.ID != id || len(response.Result) == 0 || len(response.Error) != 0 {
			t.Fatalf("unexpected response: %+v", response)
		}
	}
}
