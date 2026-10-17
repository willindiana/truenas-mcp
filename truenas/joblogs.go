package truenas

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"time"
)

// JobLogs downloads an existing job's logs without exposing its download token.
func (c *Client) JobLogs(id int) ([]byte, error) {
	if id < 1 {
		return nil, fmt.Errorf("job_id must be positive")
	}
	raw, err := c.Call("core.job_download_logs", id, "job.log", false)
	if err != nil {
		return nil, fmt.Errorf("job logs unavailable (job may have expired or have no logs): %w", err)
	}
	var path string
	if json.Unmarshal(raw, &path) != nil {
		return nil, fmt.Errorf("invalid job log download response")
	}
	return c.downloadLog(path)
}

// downloadLog accepts only same-host download paths and never returns token-bearing errors.
func (c *Client) downloadLog(path string) ([]byte, error) {
	urls, err := c.buildConnectionURLs()
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(urls[0])
	if err != nil {
		return nil, fmt.Errorf("invalid TrueNAS endpoint")
	}
	ref, err := url.Parse(path)
	if err != nil || ref.IsAbs() || ref.Host != "" || ref.User != nil || ref.Fragment != "" || !strings.HasPrefix(ref.Path, "/_download/") || pathpkg.Clean(ref.Path) != ref.Path || strings.Contains(ref.Path, "\\") {
		return nil, fmt.Errorf("invalid job log download path")
	}
	base.Scheme = "https"
	transport := &http.Transport{TLSClientConfig: c.tlsConfig}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{
		Transport: transport, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := httpClient.Get(base.ResolveReference(ref).String())
	if err != nil {
		// URL errors include the bearer download token; never return them.
		return nil, fmt.Errorf("job log download failed (connection, TLS, or timeout)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("job log download returned HTTP %d", resp.StatusCode)
	}
	// ponytail: cap downloads at 16 MiB; add streaming tail support if larger logs are needed.
	const maxBytes = 16 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed reading job log body")
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("job log exceeds 16 MiB download limit; use TrueNAS UI to download")
	}
	return data, nil
}
