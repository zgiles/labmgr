# Lab Manager

Lab Manager controls and watches the physical servers in a lab: power through each server's BMC (Redfish) and its PDU outlets (APC over SNMP, or simple HTTP PDUs), network boot through iPXE, and a serial console in the browser. It's one Go binary with the web UI embedded.

## Features

### Power
- **Planned operations**: Power on, shut down, restart, power off, and power cycle are planned from the server's current state into visible steps (outlets on → wait for the BMC → power on → wait until the system reports on), run one at a time per server, and retried with backoff where it makes sense.
- **Waits on reality, not timers**: steps wait for the BMC to answer or the system to report its new power state, with a timeout, instead of sleeping a fixed time and hoping.
- **Scheduling**: schedule any operation for later; it is planned when it runs, from the state at that moment.
- **Safe after restarts**: operations and history are stored on disk. An operation that was running when Lab Manager stopped is marked interrupted, never resumed blindly.

### Network boot
- **Boot images** (iPXE chain URLs, inline iPXE scripts, or local disk) assigned per server.
- **Next boot only**: serve an image for one network boot, then revert to what the server had before.
- **Reinstall**: one action that serves an installer once, tells the BMC to network boot once, restarts (or powers on) the server, and waits until it fetches its script.
- **BMC boot device**: network, BIOS setup, or disk on the next boot.
- **Preview** of the exact iPXE script each server gets, and when it last network booted.

### Console and diagnostics
- **Serial console in the browser**: Lab Manager logs in to the BMC over SSH and attaches to the host's serial port (`console com2` on iDRAC, `console 1` on XCC, configurable). Viewers share one session; recent output is kept and downloadable.
- **Graphical console**: a link to the BMC's own web KVM.
- **Hardware details and BMC event log** (model, serial, BIOS, health; watchdog resets, PSU faults, PCIe errors).

### Interface
- Live updates over Server-Sent Events, with a step track for every running operation.
- A dense server table with a side panel per server (Overview, Boot, Activity, Console), deep-linkable as `#/servers/<id>/<tab>`.
- Activity history for every action: who did what, when, and how it went.
- Light and dark themes; works on a phone.

## Quick start

Prerequisites: Go 1.26+, Node.js 20.19+ or 22.12+, network access to your BMCs (HTTPS 443, SSH 22) and PDUs (SNMP 161).

```bash
make build                                  # frontend (typecheck + bundle) and backend, embedded
cp config/servers.yaml.example config/servers.yaml
$EDITOR config/servers.yaml                 # your servers, BMC credentials, PDUs
./labmgr -config config/servers.yaml
```

Open http://localhost:8098.

Flags:

| Flag | Default | |
|---|---|---|
| `-config` | `config/servers.yaml` | Server and PDU definitions. Reloaded automatically when the file changes, or on `SIGHUP`. |
| `-listen` | `0.0.0.0:8098` | Address to serve on. |
| `-data` | next to the config, e.g. `config/servers.db` | Operations and activity history. |
| `-debug` | off | Log every request and driver detail. |
| `-os-data` | next to the config, e.g. `config/servers.os.json` | Boot images and assignments. |

To run it as a service, see [DEPLOYMENT.md](DEPLOYMENT.md): a systemd unit is in `deploy/labmgr.service`, and the repository is a Nix flake with a NixOS module (`services.labmgr`).

## Development

```bash
make test                                   # Go tests (fake hardware) with -race, and the TypeScript typecheck

# Terminal 1: backend
cd backend && go run . -config ../config/servers.yaml

# Terminal 2: frontend with hot reload on :8099, proxying /api to the backend
cd frontend && npm run dev
# point it elsewhere with LABMGR_API=http://host:8098, or change port with PORT=...
```

The engine tests run against a simulated lab (outlets feed servers, a BMC goes dark when its outlets are off) with time scaled down, so they're fast and never touch real hardware.

## Configuration

```yaml
servers:
  - id: "server1"
    name: "Web Server 1"
    description: "Production web server"
    ipmi:                       # the BMC, reached over Redfish (HTTPS) and SSH
      host: "192.168.1.100"
      username: "admin"
      password: "password"
    pdu_outlets:                # optional; "pdu-id:outlet"
      - "pdu1:1"
      - "pdu1:2"
    console:                    # optional
      command: "console com2"   # typed at the BMC prompt; detected from the vendor if omitted
      ssh_port: 22
      web_url: "https://192.168.1.100"
    metadata:
      role: "webserver"
      mac: "aa:bb:cc:dd:ee:ff"  # needed for network boot; any case or separator
      hostname: "web1.lab.local"

pdus:
  - id: "pdu1"
    name: "Rack 1 PDU"
    host: "192.168.1.200"
    type: "apc"                 # SNMP; anything else uses the HTTP interface
    snmp_community: "private"
    snmp_version: "v2c"         # v1 or v2c

delays:
  pdu_to_power_on_seconds: 10   # after outlets come on, before waiting for the BMC
  power_off_to_pdu_seconds: 30  # after the system is off, before cutting outlets
  between_ipmi_seconds: 2       # power cycle pauses twice this between off and on
  between_pdu_seconds: 3        # between consecutive outlet switches (minimum 3)
```

A server needs a BMC (`ipmi.host`), PDU outlets, or both. Credentials never leave the server: the API strips passwords and SNMP communities.

## API

`/api/v2` is the current API; `/api/v1` is kept working for older scripts. See [API.md](API.md).

## iPXE integration

Point your DHCP server's iPXE clients at `http://lab-manager:8098/static/boot.ipxe` (or chain straight to `/api/v1/autoexec.ipxe?mac=${net0/mac}`). Lab Manager finds the server by MAC and answers with, in order of priority:

1. the boot image assigned to it (a one-time assignment wins, then reverts),
2. `metadata.next-path` (chain to a URL),
3. `metadata.boot-template` (an inline iPXE script),
4. a local disk boot.

Templates use Go template syntax with `{{.MAC}}`, `{{.Hostname}}`, `{{.Role}}`, `{{.ServerName}}`, `{{.ServerID}}`, `{{.IncomingServerIP}}`, and `{{.Metadata.key}}`.

Chain loading:

```yaml
metadata:
  next-path: "https://boot.netboot.xyz/menu.ipxe"
```

Inline script (Debian netboot installer):

```yaml
metadata:
  boot-template: |
    #!ipxe
    set mirror http://deb.debian.org/debian/dists/stable/main/installer-amd64/current/images/netboot/debian-installer/amd64
    kernel ${mirror}/linux auto=true priority=critical hostname={{.Hostname}} url=http://lab.example.com/preseed/{{.Role}}.cfg
    initrd ${mirror}/initrd.gz
    boot
```

The same scripts can be saved as boot images in the UI (Boot images → New boot image) and assigned without editing the config.

## Architecture

**Backend (Go, `backend/`)**
- `config.go`: config store; parsed once, validated, hot reloaded.
- `state.go`, `poller.go`: observed state per server (BMC link and system power, PDU link and each outlet), polled on per-device schedules (faster while an operation runs); one SNMP query per PDU. The display state (`on`, `off`, `powered`, `powering_on`, …) is derived in one place.
- `engine.go`, `plans.go`, `execute.go`, `reinstall.go`: operations. Planners turn a request plus current state into steps; the engine runs them (one operation per server), with retries, cancellation, scheduling, and persistence.
- `redfish.go`, `pdu.go`, `hardware.go`: drivers behind interfaces (Redfish via gofish with basic auth so polling doesn't use up BMC sessions; APC SNMP; HTTP PDUs).
- `console.go`: shared SSH serial console sessions with scrollback, bridged to WebSockets.
- `store.go` (bbolt), `events.go` (SSE hub), `api.go` (v2), `api_v1.go` (compatibility), `boot.go` (iPXE).

**Frontend (Vue 3 + TypeScript, `frontend/`)**
- `store.ts`: one live store fed by `/api/v2/stream`.
- `views/`: Servers, Boot images, Activity. `components/`: the server panel and its tabs, step track, dialogs.
- `styles/app.css`: design tokens (light and dark).

## Troubleshooting

**A server shows Unreachable**: the BMC isn't answering HTTPS. The panel shows the reason (no route to host, connection refused, login rejected). If the server has PDU outlets and they're off, its BMC is dark too; that shows as Off, not Unreachable.

**Powered (amber)**: the outlets are live but the BMC isn't responding. Power cycle clears a hung BMC.

**Console connects but stays blank**: the BMC attached to the serial port, but the host isn't writing to it. Point the OS console at the BMC's serial port (e.g. `console=ttyS1,115200` on iDRAC, `ttyS0` on many XCCs) or enable a serial getty. Some BMCs also need serial-over-LAN enabled in their settings.

**An operation failed**: open the server's Activity tab. Each step shows its attempts and the exact error; Try again re-plans from the current state.

## License

MIT; see [LICENSE](LICENSE).
