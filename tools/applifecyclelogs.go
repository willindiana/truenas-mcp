package tools

import (
	"encoding/json"
	"fmt"

	"github.com/truenas/truenas-mcp/truenas"
)

func handleAppLifecycleLogs(client *truenas.Client, args map[string]interface{}) (string, error) {
	for key := range args {
		if key != "tail_lines" {
			return "", fmt.Errorf("unsupported argument: %s; only tail_lines is accepted", key)
		}
	}
	tail, err := boundedJobInt(args, "tail_lines", 200, 5000)
	if err != nil {
		return "", err
	}
	data, err := client.AppLifecycleLogs()
	if err != nil {
		return "", err
	}
	text, truncated, err := logTail(data, tail)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(map[string]interface{}{"scope": "all_apps", "logs": text, "truncated": truncated})
	if len(result) > 1<<20 {
		return "", fmt.Errorf("selected log tail exceeds 1 MiB output limit; request fewer tail_lines")
	}
	return string(result), err
}
