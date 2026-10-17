## Summary

Adds two read-only tools for retrieving app container logs, plus the
subscription plumbing in the API client needed to consume them.

- **`app_containers`** — lists an app's containers with service name, state,
  image and container ID.
- **`app_logs`** — returns the last N log lines with timestamps, for a single
  container or all of them.

Verified against **TrueNAS 25.04.1** on a live system.

## Why the client needed changes

TrueNAS exposes container logs through `app.container_log_follow`, which is a
*dynamic event source* rather than a request/response method: you subscribe,
then it streams frames until you unsubscribe. The client only supported
`Call`, so this PR adds:

- `Subscribe`/`Unsubscribe` over `core.subscribe`
- Event dispatch in `readLoop`, keyed on the frame's `collection` field rather
  than a request ID
- Buffered per-subscription channels with **non-blocking** delivery, so one slow
  consumer cannot stall every concurrent request sharing the socket
- `closeAllSubs` on disconnect, since server-side subscriptions die with the
  connection

## Design note: two container-resolution paths

This is the part most worth reviewing.

`app.container_ids` is the cheap call, but it returns `{}` for **CRASHED and
STOPPED apps** — even though those containers still exist and their logs remain
readable. `app.get_instance` → `active_workloads.container_details` does report
them.

So `resolveAppContainers` tries the cheap path first and falls back:

| App state | `app.container_ids` | `app.get_instance` |
|---|---|---|
| Running | container IDs | container IDs |
| Crashed / Stopped | `{}` | container IDs |

Without that fallback, `app_logs` would work fine on healthy apps and fail on
exactly the broken ones it is most useful for. This was confirmed against three
genuinely crashed apps (`subgen`, `cura`, `nsx-library-manager`) — all three
returned `{}` from `app.container_ids` and full log output via the fallback.

Log reads are bounded three ways, since the source streams until cancelled:
stop at `tail_lines`, stop after an idle gap, stop at a hard deadline.

## Behaviours to be aware of

- **Chunked, not line-based output.** `app.container_log_follow` emits per
  `read()` chunk rather than per line, so one logical line can arrive as several
  entries. Entries are printed as received. Reassembling them would mean
  guessing chunk boundaries and corrupting output — happy to revisit if
  maintainers would rather have best-effort stitching.
- **Per-entry truncation** at 2000 bytes, so a runaway container cannot flood
  the response.
- Event routing matches `collection` exactly, then falls back to an event-name
  prefix match in case the server echoes subscription params in a different
  order or spacing.

## Testing

- `tools/applogs_test.go` — payload decoding across both event shapes,
  timestamp formatting, truncation bounds, missing-argument handling.
- `truenas/events_test.go` — subscription key determinism, exact and
  prefix-match routing, non-blocking dispatch under an unread subscriber,
  and `closeAllSubs`.

`go build`, `go vet`, `gofmt` and `go test ./...` all clean. The truncation test
caught a real bug during development (the bound was skipped when an event had no
timestamp); fixed in `formatLogLine`.

Manual verification against the live system:

| Case | Result |
|---|---|
| `app_logs` on CRASHED app (`subgen`, `cura`) | log lines returned from exited container |
| `app_logs` with `container` filter (`immich`/server) | correct single container |
| `app_containers` multi-container (`immich`) | all 4 containers with state |
| `app_logs` unknown container name | clear error |
| `app_logs` missing `app_name` | clear error |
| Tool count | 52 → 54, no regressions |

## Checklist

- [x] `go build ./...`
- [x] `go vet ./...`
- [x] `gofmt`
- [x] `go test ./...`
- [x] Verified against a live TrueNAS 25.04.1 instance
- [x] No new configuration surface or write operations