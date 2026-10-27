package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/truenas/truenas-mcp/truenas"
)

// Log tailing bounds. tail_lines is honoured exactly when the source has that
// many lines; otherwise we stop on idle or at the hard deadline.
const (
	defaultLogTail      = 200
	maxLogTail          = 5000
	logIdleTimeout      = 3 * time.Second
	logHardDeadline     = 30 * time.Second
	logFirstEventGrace  = 10 * time.Second
	logTimestampMaxLen  = 40
	logOversizeDataWarn = 2000
)

// logEventFields is the payload shape of an app.container_log_follow event.
type logEventFields struct {
	Data      string `json:"data"`
	Timestamp string `json:"timestamp"`
}

// resolveAppContainers returns the containers belonging to an app.
//
// Two sources are needed because they disagree for stopped apps:
//   - app.container_ids is the cheap path and works for running apps.
//   - For CRASHED/STOPPED apps it returns {}, even though the containers still
//     exist and their logs remain readable, so we fall back to
//     app.get_instance -> active_workloads.container_details.
func resolveAppContainers(client *truenas.Client, appName string) ([]map[string]interface{}, error) {
	result, err := client.Call("app.container_ids", appName)
	if err != nil {
		// Fall through to the get_instance path rather than failing outright.
		result = nil
	}

	var containers []map[string]interface{}
	if len(result) > 0 {
		var raw map[string]map[string]interface{}
		if err := json.Unmarshal(result, &raw); err == nil {
			for id, info := range raw {
				entry := map[string]interface{}{"id": id}
				for k, v := range info {
					entry[k] = v
				}
				if _, ok := entry["id"]; !ok {
					entry["id"] = id
				}
				containers = append(containers, entry)
			}
		}
	}

	if len(containers) > 0 {
		sortContainers(containers)
		return containers, nil
	}

	// Fallback: crashed/stopped apps still report their containers here.
	inst, err := client.Call("app.get_instance", appName)
	if err != nil {
		return nil, fmt.Errorf("app %q not found: %w", appName, err)
	}

	var app map[string]interface{}
	if err := json.Unmarshal(inst, &app); err != nil {
		return nil, fmt.Errorf("failed to parse app record for %q: %w", appName, err)
	}

	workloads, _ := app["active_workloads"].(map[string]interface{})
	details, _ := workloads["container_details"].([]interface{})
	for _, d := range details {
		entry, ok := d.(map[string]interface{})
		if !ok {
			continue
		}
		containers = append(containers, entry)
	}

	sortContainers(containers)
	return containers, nil
}

// sortContainers gives map-backed results a stable order.
func sortContainers(containers []map[string]interface{}) {
	sort.SliceStable(containers, func(i, j int) bool {
		si, _ := containers[i]["service_name"].(string)
		sj, _ := containers[j]["service_name"].(string)
		if si != sj {
			return si < sj
		}
		ii, _ := containers[i]["id"].(string)
		ij, _ := containers[j]["id"].(string)
		return ii < ij
	})
}

// handleAppContainers lists the containers for an app so callers can pick one
// to read logs from, without having to know TrueNAS internals.
func handleAppContainers(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, _ := args["app_name"].(string)
	if appName == "" {
		return "", fmt.Errorf("app_name is required")
	}

	containers, err := resolveAppContainers(client, appName)
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return fmt.Sprintf("No containers found for app %q. It may be stopped or still deploying.", appName), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "App %q has %d container(s):\n\n", appName, len(containers))
	for _, c := range containers {
		id, _ := c["id"].(string)
		name, _ := c["service_name"].(string)
		state, _ := c["state"].(string)
		image, _ := c["image"].(string)
		if state == "" {
			state = "unknown"
		}
		fmt.Fprintf(&b, "- service=%s state=%s\n", name, state)
		fmt.Fprintf(&b, "  container_id=%s\n", id)
		if image != "" {
			fmt.Fprintf(&b, "  image=%s\n", image)
		}
	}

	fmt.Fprintf(&b, "\nUse app_logs with app_name=%s to read logs.", appName)
	return b.String(), nil
}

// handleAppLogs reads container logs for an app.
//
// It subscribes to app.container_log_follow, which is a dynamic event source
// rather than a request/response method: it streams events until cancelled.
// We therefore bound the read three ways - stop once tail_lines events have
// arrived, stop after an idle gap, and stop at a hard deadline.
func handleAppLogs(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, _ := args["app_name"].(string)
	if appName == "" {
		return "", fmt.Errorf("app_name is required")
	}

	tail := defaultLogTail
	if v, ok := args["tail_lines"]; ok {
		t, ok := v.(float64)
		if !ok || t != math.Trunc(t) || t < 1 || t > maxLogTail {
			return "", fmt.Errorf("tail_lines must be an integer from 1 to %d", maxLogTail)
		}
		tail = int(t)
	}

	containerFilter, _ := args["container"].(string)

	containers, err := resolveAppContainers(client, appName)
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", fmt.Errorf("no containers found for app %q (it may be stopped or still deploying)", appName)
	}

	selected := containers
	if containerFilter != "" {
		selected = nil
		for _, c := range containers {
			name, _ := c["service_name"].(string)
			id, _ := c["id"].(string)
			if name == containerFilter || strings.HasPrefix(id, containerFilter) {
				selected = append(selected, c)
			}
		}
		if len(selected) == 0 {
			return "", fmt.Errorf("container %q not found in app %q", containerFilter, appName)
		}
	}

	var b strings.Builder
	totalLines := 0

	for _, c := range selected {
		cid, _ := c["id"].(string)
		svc, _ := c["service_name"].(string)
		if cid == "" {
			continue
		}
		if svc == "" {
			svc = cid[:min(len(cid), 12)]
		}

		lines, err := collectContainerLogs(client, appName, cid, tail)
		if err != nil {
			fmt.Fprintf(&b, "=== %s: failed to read logs: %v ===\n\n", svc, err)
			continue
		}

		if len(selected) > 1 {
			fmt.Fprintf(&b, "=== %s (%d lines) ===\n", svc, len(lines))
		}
		for _, l := range lines {
			b.WriteString(l)
			b.WriteString("\n")
		}
		totalLines += len(lines)
		if len(selected) > 1 {
			b.WriteString("\n")
		}
	}

	if totalLines == 0 {
		return fmt.Sprintf("No log output returned for app %q. The container may not have produced output yet.", appName), nil
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

// collectContainerLogs subscribes to one container's log stream and collects up
// to tailLines entries.
func collectContainerLogs(client *truenas.Client, appName, containerID string, tailLines int) ([]string, error) {
	params := map[string]interface{}{
		"app_name":     appName,
		"container_id": containerID,
		"tail_lines":   tailLines,
	}

	events, err := client.Subscribe("app.container_log_follow", params)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := client.Unsubscribe("app.container_log_follow", params); err != nil {
			// A failed teardown must not mask collected output.
			return
		}
	}()

	var lines []string
	deadline := time.After(logHardDeadline)
	idle := time.NewTimer(logFirstEventGrace)
	defer idle.Stop()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return lines, nil
			}

			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(logIdleTimeout)

			data := decodeLogData(ev)
			if data == "" {
				continue
			}
			lines = append(lines, formatLogLine(data, ev.Fields))

			if len(lines) >= tailLines {
				return lines, nil
			}

		case <-idle.C:
			// No output during the first-event grace or after the last event.
			return lines, nil

		case <-deadline:
			return lines, nil
		}
	}
}

// decodeLogData extracts the log text from an event frame. The middleware
// sends the line in fields.data; result is accepted as a fallback.
func decodeLogData(ev truenas.Event) string {
	raw := ev.Fields
	if len(raw) == 0 {
		raw = ev.Result
	}
	if len(raw) == 0 {
		return ""
	}

	var f logEventFields
	if err := json.Unmarshal(raw, &f); err != nil {
		return ""
	}
	return strings.TrimRight(f.Data, "\n")
}

// formatLogLine renders one log entry. Docker delivers stdout and stderr as
// separate streams, so a single logical line can arrive as multiple chunks;
// entries are printed as received rather than reassembled, since guessing
// chunk boundaries would corrupt the output.
func formatLogLine(data string, rawFields json.RawMessage) string {
	var f logEventFields
	ts := ""
	if len(rawFields) > 0 && json.Unmarshal(rawFields, &f) == nil {
		ts = f.Timestamp
	}

	// Bound the payload before the early return below, so an oversized line is
	// truncated whether or not the event carried a timestamp.
	if len(data) > logOversizeDataWarn {
		data = data[:logOversizeDataWarn] + "... [truncated]"
	}
	if ts == "" {
		return data
	}
	if len(ts) > logTimestampMaxLen {
		ts = ts[:logTimestampMaxLen]
	}
	return fmt.Sprintf("%s %s", ts, data)
}
