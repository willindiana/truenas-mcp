package truenas

import (
	"encoding/json"
	"errors"
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
		return nil, jobLogsError(id, err)
	}
	var path string
	if json.Unmarshal(raw, &path) != nil {
		return nil, fmt.Errorf("invalid job log download response")
	}
	return c.downloadLog(path)
}

// jobLogsError keeps only the server's message; the middleware trace is
// kilobytes of stack frames. An unknown job id surfaces as a KeyError.
func jobLogsError(id int, err error) error {
	var apiErr *APICallError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("job %d logs unavailable: %w", id, err)
	}
	var trace struct {
		Class string `json:"class"`
	}
	if json.Unmarshal([]byte(apiErr.Trace), &trace) == nil && trace.Class == "KeyError" {
		return fmt.Errorf("job %d not found (it may have expired from job history)", id)
	}
	return fmt.Errorf("job %d logs unavailable: %s", id, apiErr.Message)
}

// downloadLog accepts only same-host download paths and never returns token-bearing errors.
func (c *Client) downloadLog(path string) ([]byte, error) {
	base, err := c.endpointURL()
	if err != nil {
		return nil, err
	}
	proxy, err := c.proxyFor(base)
	if err != nil {
		return nil, err
	}
	ref, err := url.Parse(path)
	if err != nil || ref.IsAbs() || ref.Host != "" || ref.User != nil || ref.Fragment != "" || !strings.HasPrefix(ref.Path, "/_download/") || pathpkg.Clean(ref.Path) != ref.Path || strings.Contains(ref.Path, "\\") {
		return nil, fmt.Errorf("invalid job log download path")
	}
	base.Scheme = "https"
	transport := &http.Transport{TLSClientConfig: c.tlsConfig}
	if proxy != nil {
		transport.Proxy = http.ProxyURL(dialerProxy(proxy.url))
	}
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
