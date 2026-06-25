# wim-prompt-agent

Local collection agent for WIM Backoffice prompt insights.

## What and Why

`wim-prompt-agent` runs in the background on each developer machine and
periodically reads settled Claude Code sessions from
`~/.claude/projects/**/*.jsonl`. It redacts secrets, persists events to a
local disk queue, and uploads them in batches to the WIM backend
(`PATCH /api/v1/prompt-insights/events`).

This design keeps the subscription flat-rate (no per-token API cost) and is
vendor-neutral: the adapter layer supports multiple tools; Claude Code is P1,
others (Codex, Cursor, …) are P2+.

## Build

```bash
# macOS (Apple Silicon or Intel)
GOOS=darwin GOARCH=arm64 go build -o bin/wim-prompt-agent ./cmd/wim-prompt-agent
GOOS=darwin GOARCH=amd64 go build -o bin/wim-prompt-agent ./cmd/wim-prompt-agent

# Linux
GOOS=linux GOARCH=amd64 go build -o bin/wim-prompt-agent ./cmd/wim-prompt-agent
```

Go 1.22+ required. No external dependencies (`go.mod` stdlib-only).

## Commands

### `install` — register periodic daemon

```bash
./wim-prompt-agent install
```

- **macOS**: installs a `launchd` user agent (`co.wimcorp.promptagent`) that
  runs `run-once` every 10 minutes.
- **Linux**: installs a `systemd --user` timer (`wim-prompt-agent.timer`)
  with the same interval.

The binary path is resolved at install time via `os.Executable()`; move the
binary before running `install`.

To remove:

```bash
./wim-prompt-agent uninstall
```

### `enroll` — register this device with the WIM backend

```bash
WIM_PROMPT_BASE_URL=https://staging-backoffice-api.wimcorp.co.kr ./wim-prompt-agent enroll
```

`enroll` runs the Google OAuth 2.0 **PKCE loopback** flow (opens your browser,
captures the auth code on a `127.0.0.1` callback, exchanges it for a Google
`id_token`), calls `POST /api/v1/prompt-insights/enroll` with that token,
receives a device-scoped bearer token, and stores it in the OS keychain.

> **Setup required**: enroll needs a Google Cloud **"Desktop app" OAuth client**.
> Set its credentials before running:
> ```bash
> export WIM_PROMPT_GOOGLE_CLIENT_ID=<desktop-client-id>
> export WIM_PROMPT_GOOGLE_CLIENT_SECRET=<desktop-client-secret>   # non-confidential for desktop clients
> ./wim-prompt-agent enroll
> ```
> The backend must also accept this client as an audience — set Vault
> `OAUTH2_GOOGLE_AGENT_CLIENT_ID` to the same desktop client id (see backend PR).
> Without these, enroll exits with "OAuth client not configured" (and uploads
> have no token).

### `run-once` — scan, redact, and upload once

```bash
WIM_PROMPT_BASE_URL=https://staging-backoffice-api.wimcorp.co.kr ./wim-prompt-agent run-once
```

Runs the full pipeline once:

1. Scan `~/.claude/projects/**/*.jsonl` for new settled turns (prompt +
   response pairs where the next user message or file idle-timeout confirms
   the turn is complete).
2. Redact known secret patterns (`sk-…`, PEM keys, GitHub tokens, AWS keys).
3. Enqueue events to `~/.wim-prompt-agent/queue/` (disk-persistent).
4. Advance the per-file byte offset in `~/.wim-prompt-agent/state.json` (only
   after enqueue succeeds — no event loss on crash).
5. Drain the queue: upload to backend in batches of 100. Transient failures
   leave queue files on disk for the next run (automatic retry).

### `status` — show current configuration

```bash
./wim-prompt-agent status
```

Prints agent version, data directory, base URL, scan interval, and OS.

## Configuration

| Environment variable             | Default                                  | Description                                       |
|----------------------------------|------------------------------------------|---------------------------------------------------|
| `WIM_PROMPT_BASE_URL`            | `https://staging-backoffice-api.wimcorp.co.kr` | Backend base URL for enroll and upload calls. |
| `WIM_PROMPT_GOOGLE_CLIENT_ID`    | (unset)                                  | Desktop OAuth client id (required for `enroll`).  |
| `WIM_PROMPT_GOOGLE_CLIENT_SECRET`| (unset)                                  | Desktop OAuth client secret (non-confidential).   |
| `WIM_PROMPT_GOOGLE_HD`           | `wimcorp.co.kr`                          | Google `hd` hosted-domain hint for the login.     |

All other settings (scan interval, idle cutoff, data directory) are compiled-in
defaults (`internal/config/config.go`). No config file is required.

## Known P1 Constraints

1. **`enroll` needs a Google "Desktop app" OAuth client**: the PKCE loopback
   flow is implemented, but until the desktop OAuth client is created and its
   id/secret are set (`WIM_PROMPT_GOOGLE_CLIENT_ID`/`_SECRET`) AND the backend
   accepts that audience (Vault `OAUTH2_GOOGLE_AGENT_CLIENT_ID`), enroll cannot
   obtain a token and uploads have no auth. This is the one gate before live
   end-to-end collection.

2. **Linux requires `libsecret-tools`**: the Linux keychain backend calls
   `secret-tool` (part of the `libsecret-tools` / `libsecret` package).
   Install it before running `enroll` or `run-once`:
   ```bash
   sudo apt-get install libsecret-tools   # Debian/Ubuntu
   sudo dnf install libsecret             # Fedora/RHEL
   ```

3. **Windows is P2**: `install`, `enroll`, and the keychain backend are not
   implemented for Windows. Use `run-once` manually via Task Scheduler if
   needed.

## Data Directory

All runtime state lives in `~/.wim-prompt-agent/` (created automatically):

```
~/.wim-prompt-agent/
  state.json        # per-file byte offsets (scanner progress)
  queue/            # disk-persistent event batches (auto-drained)
```

## Testing

```bash
go test ./...
```

All packages are tested, including an integration E2E test in
`internal/e2e/` that wires the full pipeline against a mock HTTP server
in a temp directory (no keychain, no daemon required).
