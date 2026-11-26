# Development

You need Go 1.26+, Node.js 20.19+ or 22.12+, and make. With Nix, `nix develop` provides
them. [Building](building.md) covers release builds, Nix, and replacing a running binary.

```bash
make test        # Go tests with -race (against simulated hardware) and the TypeScript typecheck
make build       # frontend + backend → ./labmgr
make release     # → releases/labmgr-<git describe>-linux-amd64; never overwrites an existing file
```

Run the backend and the frontend's dev server side by side:

```bash
# Terminal 1: the backend, against a simulated lab (or -config ../config/servers.yaml)
cd backend && go run . -demo

# Terminal 2: the frontend with hot reload on :8099, proxying /api to the backend
cd frontend && npm run dev
# LABMGR_API=http://host:8098 points it at another backend; PORT=... changes its port
```

The engine tests run against a simulated lab, with time scaled down, so they're fast and
never touch real hardware. In the simulation, outlets feed servers, and a BMC goes dark
when its outlets are off. `-demo` uses a similar simulation, with fake power readings and
consoles, for trying the UI.

## Layout

**Backend (Go, `backend/`)**

| File | |
|---|---|
| `main.go`, `embedded.go` | Flags, wiring, and routes. The frontend is embedded from `backend/static/`. |
| `config.go` | The config store: parsed once, validated, and reloaded when the file changes. |
| `state.go`, `poller.go` | Observed state per server (BMC link, system power, PDU link, each outlet), polled on per-device schedules, faster while an operation runs. The display state (`on`, `off`, `powered`, `powering_on`, …) is derived in one place. |
| `engine.go`, `plans.go`, `execute.go`, `reinstall.go` | Operations. Planners turn a request and the current state into steps. The engine runs them, one operation per server, with retries, cancellation, scheduling, and persistence. |
| `hardware.go`, `redfish.go`, `redfish_session.go`, `pdu.go` | Drivers behind interfaces: Redfish via gofish (one shared login session per BMC), APC over SNMP, and HTTP PDUs. |
| `console.go`, `console_api.go`, `console_capture.go` | Shared SSH serial console sessions with scrollback, bridged to WebSockets, and the recordings. |
| `kvm_xcc.go` | The hosted Lenovo XCC viewer and its WebSocket relay. |
| `boot.go`, `os_manager.go`, `os_api.go` | iPXE scripts, boot images, and assignments. |
| `store.go` | bbolt: operations, activity, and power history. |
| `events.go` | The Server-Sent Events hub. |
| `api.go`, `agent_api.go`, `api_v1.go` | `/api/v2`, its conveniences for scripts (name lookup, `?wait=`, text output), and `/api/v1` compatibility. |
| `demo.go` | The `-demo` simulated lab. |

**Frontend (Vue 3 + TypeScript + Vite, `frontend/`)**

| Path | |
|---|---|
| `src/store.ts` | One live store fed by `/api/v2/stream`. |
| `src/actions.ts` | Every server action and its confirmation, shared by the table and the panel. |
| `src/views/` | Servers, Boot images, Activity. |
| `src/components/` | The server panel and its tabs, the step track, charts, and dialogs. |
| `src/styles/app.css` | Design tokens, light and dark. |

## Releases

Push a `v*` tag to build Linux amd64 and arm64 binaries, with `SHA256SUMS`, into a GitHub
release. The release notes are taken from that version's section of
[CHANGELOG.md](../CHANGELOG.md).

If the Nix build's dependency hashes go stale after changing `go.mod` or
`frontend/package-lock.json`, update `vendorHash` or `npmDepsHash` in `nix/package.nix`.
`nix build` prints the right one.
