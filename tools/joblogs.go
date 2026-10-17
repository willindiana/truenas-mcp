package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/truenas/truenas-mcp/truenas"
)

func boundedJobInt(args map[string]interface{}, name string, fallback, max int) (int, error) {
	v, exists := args[name]
	if !exists {
		return fallback, nil
	}
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || n < 1 || n > float64(max) || math.Trunc(n) != n {
		return 0, fmt.Errorf("%s must be an integer between 1 and %d", name, max)
	}
	return int(n), nil
}

func handleJobLogs(client *truenas.Client, args map[string]interface{}) (string, error) {
	id, err := boundedJobInt(args, "job_id", 0, 2147483647)
	if err != nil {
		return "", err
	}
	if id == 0 {
		return "", fmt.Errorf("job_id is required")
	}
	tail, err := boundedJobInt(args, "tail_lines", 200, 5000)
	if err != nil {
		return "", err
	}
	data, err := client.JobLogs(id)
	if err != nil {
		return "", err
	}
	text, truncated, err := logTail(data, tail)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(map[string]interface{}{"job_id": id, "logs": text, "truncated": truncated})
	return string(result), err
}

func logTail(data []byte, tail int) (string, bool, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	truncated := len(lines) > tail
	if truncated {
		lines = lines[len(lines)-tail:]
	}
	text := strings.Join(lines, "\n")
	if len(text) > 1<<20 {
		return "", false, fmt.Errorf("selected log tail exceeds 1 MiB; request fewer tail_lines")
	}
	return text, truncated, nil
}

func handleQueryReplicationTasks(client *truenas.Client, args map[string]interface{}) (string, error) {
	id, err := boundedJobInt(args, "task_id", 0, 2147483647)
	if err != nil {
		return "", err
	}
	filters := []interface{}{}
	if id != 0 {
		filters = append(filters, []interface{}{"id", "=", id})
	}
	raw, err := client.Call("replication.query", filters)
	if err != nil {
		return "", fmt.Errorf("failed to query replication tasks: %w", err)
	}
	var tasks []map[string]interface{}
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return "", fmt.Errorf("failed to parse replication tasks: %w", err)
	}
	result := make([]map[string]interface{}, 0, len(tasks))
	for _, task := range tasks {
		item := map[string]interface{}{}
		// Allowlist avoids returning nested SSH credentials or encryption keys.
		for _, key := range strings.Fields("id name direction transport source_datasets target_dataset recursive exclude enabled state compression large_block embed compressed retries speed_limit schedule auto retention_policy lifetime_value lifetime_unit readonly allow_from_scratch hold_pending_snapshots") {
			if value, ok := task[key]; ok {
				item[key] = value
			}
		}
		result = append(result, item)
	}
	encoded, err := json.Marshal(map[string]interface{}{"tasks": result, "task_count": len(result)})
	return string(encoded), err
}
