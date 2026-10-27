package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStartupWithoutBackend(t *testing.T) {
	if os.Getenv("TRUENAS_MCP_STARTUP_TEST") == "1" {
		os.Args = os.Args[:1]
		*truenasURL = "wss://127.0.0.1:1/api/current"
		*apiKey = "test-only"
		*apiUser = "test-only"
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

func TestCheckRequired(t *testing.T) {
	if err := checkRequired("truenas.local", "1-key", "svc-mcp"); err != nil {
		t.Fatalf("complete configuration rejected: %v", err)
	}
	for _, tc := range []struct{ url, key, user, want string }{
		{"truenas.local", "1-key", "", "--api-user"},
		{"", "1-key", "svc-mcp", "--truenas-url"},
		{"truenas.local", "", "svc-mcp", "--api-key"},
	} {
		err := checkRequired(tc.url, tc.key, tc.user)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("checkRequired(%q, key, %q) = %v; want error naming %s", tc.url, tc.user, err, tc.want)
		}
	}
}

func TestSafetyPolicy(t *testing.T) {
	envOf := func(env map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	}
	env := envOf(map[string]string{
		"TRUENAS_MCP_READ_ONLY":   "yes",
		"TRUENAS_MCP_ALLOW_TOOLS": " query_pools, ,list_alerts ",
		"TRUENAS_MCP_DENY_TOOLS":  "system_reboot",
	})
	p, err := safetyPolicy(false, nil, env)
	if err != nil || !p.ReadOnly || strings.Join(p.Allow, ",") != "query_pools,list_alerts" || strings.Join(p.Deny, ",") != "system_reboot" {
		t.Errorf("env policy = %+v, %v", p, err)
	}
	p, err = safetyPolicy(false, map[string]string{"allow-tools": "query_apps", "deny-tools": ""}, env)
	if err != nil || strings.Join(p.Allow, ",") != "query_apps" || len(p.Deny) != 0 {
		t.Errorf("flags did not override env: %+v, %v", p, err)
	}
	for _, v := range []string{"", "0", "false", "No", "off"} {
		p, _ := safetyPolicy(false, nil, envOf(map[string]string{"TRUENAS_MCP_READ_ONLY": v}))
		if p.ReadOnly {
			t.Errorf("TRUENAS_MCP_READ_ONLY=%q enabled read-only", v)
		}
	}
	if p, _ := safetyPolicy(true, nil, envOf(nil)); !p.ReadOnly {
		t.Error("--read-only ignored")
	}
	if p, err := safetyPolicy(false, nil, envOf(nil)); err != nil || p.Allow != nil {
		t.Errorf("unset allow list = %+v, %v; want no restriction", p, err)
	}

	// An allow list that is set but names no tools must fail closed.
	for _, v := range []string{"", ",", " , ", " ,, "} {
		if _, err := safetyPolicy(false, nil, envOf(map[string]string{"TRUENAS_MCP_ALLOW_TOOLS": v})); err == nil {
			t.Errorf("TRUENAS_MCP_ALLOW_TOOLS=%q accepted", v)
		}
		if _, err := safetyPolicy(false, map[string]string{"allow-tools": v}, envOf(nil)); err == nil {
			t.Errorf("--allow-tools=%q accepted", v)
		}
	}
}

// The allow/deny flags are registered without package variables (main reads
// them through flag.Visit); make sure they stay registered and documented.
func TestSafetyFlagsRegistered(t *testing.T) {
	for _, name := range []string{"read-only", "allow-tools", "deny-tools"} {
		f := flag.Lookup(name)
		if f == nil {
			t.Errorf("flag --%s is not registered", name)
			continue
		}
		if !strings.Contains(f.Usage, "TRUENAS_MCP_") {
			t.Errorf("flag --%s usage does not name its environment variable: %q", name, f.Usage)
		}
	}
}
