package truenas

import (
	"encoding/json"
	"fmt"
)

// AppLifecycleLogs reads the shared lifecycle log for all apps, not container logs.
func (c *Client) AppLifecycleLogs() ([]byte, error) {
	raw, err := c.Call("core.download", "filesystem.get", []string{"/var/log/app_lifecycle.log"}, "app_lifecycle.log", false)
	if err != nil {
		return nil, fmt.Errorf("app lifecycle logs unavailable (requires FULL_ADMIN)")
	}
	var result []json.RawMessage
	if json.Unmarshal(raw, &result) != nil || len(result) != 2 {
		return nil, fmt.Errorf("invalid app lifecycle log download response")
	}
	var jobID int
	var path string
	if json.Unmarshal(result[0], &jobID) != nil || jobID < 1 || json.Unmarshal(result[1], &path) != nil {
		return nil, fmt.Errorf("invalid app lifecycle log download response")
	}
	return c.downloadLog(path)
}
