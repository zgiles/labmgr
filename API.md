# API reference

Base URL: `http://<host>:8098/api/v2`. JSON in and out. There's no authentication; run Lab Manager on a trusted network.

Errors come back as `{"error": "message"}` with a meaningful status: `400` bad request, `404` unknown server/operation, `409` conflict (the server is busy, or already in the requested state), `502` the BMC didn't answer.

Every request that changes something is recorded in the activity history with who asked. Send `X-Labmgr-User: <name>` to name yourself; otherwise it's recorded as `api@<client-ip>`.

- [Servers](#servers)
- [Operations](#operations)
- [Live updates](#live-updates)
- [Activity](#activity)
- [BMC details](#bmc-details)
- [Power draw](#power-draw)
- [Graphical console](#graphical-console)
- [Serial console](#serial-console)
- [Network boot](#network-boot)
- [Version 1 (compatibility)](#version-1-compatibility)

## Servers

### `GET /servers`

All servers with their live state. Credentials are never included.

```json
[
  {
    "id": "node01",
    "name": "node01",
    "description": "Compute",
    "metadata": {"role": "compute", "hostname": "node01", "mac": "aa:bb:cc:dd:ee:01"},
    "bmc_host": "192.0.2.111",
    "has_bmc": true,
    "outlets": ["pdu-01:21"],
    "mac": "aa:bb:cc:dd:ee:01",
    "state": {
      "server_id": "node01",
      "state": "on",
      "power": "on",
      "bmc": "ok",
      "pdu": "ok",
      "outlets": [{"ref": "pdu-01:21", "state": "on"}],
      "operation": null,
      "last_boot": {"at": "2026-10-14T21:10:03Z", "client_ip": "192.0.2.111", "source": "local"},
      "bmc_checked_at": "2026-10-14T22:01:17Z",
      "pdu_checked_at": "2026-10-14T22:01:15Z"
    },
    "os": {"template_id": "debian-installer", "template_name": "Debian installer", "boot_method": "template", "one_shot": true, "then_template": "local-disk"},
    "console": {"web_url": "https://192.0.2.111", "serial": true}
  }
]
```

`state.state` is the one value to show people:

| State | Meaning |
|---|---|
| `on` | The system is running (or, with no BMC, all outlets are on). |
| `off` | The system is off. If its outlets are off too, the BMC is dark; that's still `off`. |
| `powered` | Outlets are on, but the BMC isn't responding. |
| `partial` | No BMC; some outlets on, some off. |
| `unreachable` | Neither the BMC nor the PDU is answering. |
| `unknown` | Not checked yet. |
| `powering_on`, `powering_off`, `shutting_down`, `restarting`, `power_cycling`, `busy` | An operation is running; `state.operation` says which step. |

`state.bmc` and `state.pdu` are `ok`, `unreachable`, `none` (not configured) or `pending`. `state.power` is what the BMC reports: `on`, `off`, or `unknown`.

### `GET /servers/:id`

One server, same shape.

### `POST /servers/:id/refresh`

Checks the server's BMC and outlets now and returns the server. `POST /refresh` does every server.

## Operations

Everything that changes a server is an operation: a list of steps planned from the server's current state. Only one runs per server at a time.

### `POST /servers/:id/operations`

```json
{"kind": "power_on"}
```

| `kind` | Extra fields | What it does |
|---|---|---|
| `power_on` | | Outlets on (if off) → wait for the BMC → BMC power on → wait until the system reports on. |
| `power_off` | | BMC force off → wait until off → cut outlets. With the BMC dark, cuts outlets only. |
| `graceful_shutdown` | | Asks the OS to shut down → waits up to 5 minutes for off. |
| `restart` | | BMC hard reset. With no reachable BMC, power cycles the outlets. |
| `graceful_restart` | | Asks the OS to reboot. |
| `power_cycle` | | Full `power_off`, a pause, then `power_on`, outlets included. Clears a hung BMC. |
| `bmc` | `action`: `on`, `off`, `reboot`, `graceful_shutdown`, `graceful_restart`, `power_cycle`, `nmi` | One raw BMC action, no sequencing. |
| `outlet` | `target`: `pdu-id:outlet`, `action`: `on`, `off`, `reboot` | One outlet. |
| `boot_override` | `target`: `pxe`, `bios`, `disk`, `cd`, `none`; `persistent`: bool | Sets the BMC's boot device for the next boot (or every boot). `none` clears it. |
| `reinstall` | `target`: boot image ID | Serves the image on the next network boot only, sets PXE once, restarts (or powers on), and waits until the server fetches its script. Afterwards the previous boot image applies again. |

Add `"scheduled_for": "2026-10-15T02:00:00Z"` to run it later. A scheduled operation is planned when it starts.

Returns `202` with the operation. `409` if the server is busy, or there's nothing to do (`"node01 is already on"`).

```json
{
  "id": "op-20261014-220315-1",
  "server_id": "node01",
  "kind": "power_on",
  "label": "Power on node01",
  "status": "running",
  "current": 0,
  "steps": [
    {"kind": "wait_bmc", "label": "Wait for the BMC to respond", "seconds": 60, "status": "running", "attempts": 0},
    {"kind": "bmc", "action": "on", "label": "Power on through the BMC", "status": "pending", "attempts": 0},
    {"kind": "wait_power", "want": "on", "seconds": 180, "label": "Wait for the system to report on", "status": "pending", "attempts": 0}
  ],
  "requested_by": "claude@10.0.0.5",
  "created_at": "2026-10-14T22:03:15Z",
  "started_at": "2026-10-14T22:03:15Z"
}
```

Operation `status`: `scheduled`, `pending`, `running`, `succeeded`, `failed`, `cancelled`, `interrupted` (Lab Manager restarted while it ran). On failure, `error` says why and the failed step has the details. A scheduled operation that finds nothing to do succeeds with a `note`.

Step `status`: `pending`, `running`, `succeeded`, `failed`, `skipped`. `attempts` counts retries of BMC and outlet actions.

### `GET /operations`

Newest first. Query: `server=<id>`, `active=true` (scheduled, pending, running), `limit` (default 100). `GET /servers/:id/operations` is the same for one server.

### `GET /operations/:id`

### `POST /operations/:id/cancel`

Cancels a scheduled operation, or stops a running one before its next step. Remaining steps are skipped.

### `POST /operations/:id/run-now`

Starts a scheduled operation now.

### `POST /operations/:id/retry`

Submits a new operation like a failed, cancelled, or interrupted one, planned from the current state.

## Live updates

### `GET /stream`

Server-Sent Events. The first event is `snapshot` (`{"servers": [...], "operations": [...]}`); then:

| Event | Data |
|---|---|
| `state` | A server's `state` object, when anything visible changes |
| `operation` | An operation, whenever it changes |
| `event` | A new activity entry |
| `os` | Boot images or assignments changed; refetch them |
| `config` | The config file was reloaded; refetch servers |

## Activity

### `GET /events`, `GET /servers/:id/events`

Newest first; `limit` defaults to 200 (100 per server).

```json
[{"id": "...", "at": "2026-10-14T22:04:19Z", "server_id": "node01", "kind": "operation",
  "message": "Power on node01 failed: BMC didn't respond within 60s: no route to host",
  "actor": "claude@10.0.0.5", "level": "error"}]
```

`kind` is `operation`, `os`, `boot` (a server fetched its iPXE script), `console`, or `config`.

## BMC details

### `GET /servers/:id/bmc`

Reads the BMC live (a few seconds):

```json
{
  "info": {"manufacturer": "Dell Inc.", "model": "PowerEdge R740xd", "serial_number": "ABC1234",
           "bios_version": "2.26.1", "health": "Critical", "bmc_model": "14G Monolithic",
           "bmc_firmware": "7.00.00.184", "graphical_kvm": true},
  "boot": {"target": "none", "enabled": "Disabled", "mode": "UEFI"}
}
```

### `GET /servers/:id/bmc/logs`

The BMC's event log, newest first, without login/logout noise. `limit` defaults to 50.

## Power draw

Lab Manager reads each responding BMC's power draw once a minute and keeps 7 days of readings. The latest is `state.watts` (and `state.watts_at`) on every server, and updates arrive over `/stream`.

### `GET /servers/:id/power?hours=24`

`{"samples": [[unix_ms, watts], ...]}`, oldest first. `hours` defaults to 1 (max 168); long ranges are averaged down to at most 600 points.

### `GET /power?hours=1`

Every server's recent readings, at most 60 points each, keyed by server ID (servers without readings are omitted). Sized for sparklines.

## Graphical console

### `POST /servers/:id/kvm`

`{"url": "...", "kind": "direct" | "login", "note": "..."}`. On Dell iDRAC9 (Enterprise license) the URL opens the HTML5 virtual console directly with a one-time login from `GetKVMSession`; other BMCs get their web UI. The URL may carry a one-time credential: the response is `no-store` and it is never logged.

## Serial console

### `GET /servers/:id/console` (WebSocket)

Query: `cols`, `rows`. Lab Manager logs in to the BMC over SSH, types the attach command (`console com2` for Dell, `console 1` for Lenovo, `vsp` for HPE, or `console.command` from the config), and relays the session.

- Binary frames carry terminal bytes both ways.
- Text frames are JSON control messages. Send `{"type": "resize", "cols": 120, "rows": 40}`. Receive `{"type": "status", "state": "connected"}`, `{"type": "status", "state": "closed", "message": "..."}`, or `{"type": "error", "message": "..."}`.

Everyone watching a server shares one BMC session. Recent output (256 KB) is replayed on connect. The session closes a minute after the last viewer leaves.

### `GET /servers/:id/console/log`

Downloads the recent console output as text.

## Network boot

### Boot images

| | |
|---|---|
| `GET /os/templates` | `{"templates": [...]}` |
| `GET /os/templates/:id` | |
| `POST /os/templates` | `{"name", "description", "boot_method": "chain" \| "template" \| "local", "next_path", "boot_template"}`; the ID is derived from the name. Template syntax is checked on save. |
| `PUT /os/templates/:id` | Same body. Built-in images can't be changed. |
| `DELETE /os/templates/:id` | Fails while any server uses it. |

### Assignments

| | |
|---|---|
| `GET /servers/:id/os` | `{"assignment", "template"}`, `404` if the server boots local disk. |
| `PUT /servers/:id/os` | `{"template_id": "...", "one_shot": false}`. With `one_shot`, the image is served once and the previous assignment returns. |
| `DELETE /servers/:id/os` | Back to local disk. |
| `POST /os/bulk-assign` | `{"server_ids": [...], "template_id": "..."}` |
| `GET /os/assignments` | All assignments with their images. |

### `GET /servers/:id/boot-script`

What the server would get if it network booted now: `{"script", "source", "source_name", "one_shot"}`. Doesn't count as a boot.

### iPXE endpoints

Outside `/api/v2`, for iPXE clients:

| | |
|---|---|
| `GET /boot.ipxe` (also `/static/boot.ipxe`) | Bootstrap script for DHCP to hand out; chains to `autoexec.ipxe` with this server's address. |
| `GET /api/v1/autoexec.ipxe?mac=<mac>` | The server's script. MACs match in any case, with `:` or `-`. |
| `GET /api/v1/boot/<mac>` | Same. |

Each fetch is recorded (`state.last_boot` and an activity entry) and consumes a one-time assignment.

## Version 1 (compatibility)

`/api/v1` predates operations. It still works, mapped onto the engine:

| v1 | Becomes |
|---|---|
| `GET /servers` | The config without credentials (`{"Servers", "PDUs", "Delays"}`). |
| `POST /servers/:id/power` `{"action": "on" \| "off" \| "reboot" \| "graceful_shutdown" \| "graceful_restart"}` | `power_on`, `power_off`, `power_cycle`, … |
| `POST /servers/:id/reboot` | `power_cycle` |
| `POST /servers/:id/ipmi` `{"action"}` | `bmc` |
| `POST /pdu/:outlet/power` `{"action"}` | `outlet` |
| `GET /servers/:id/status`, `GET /status` | `status` is the v2 display state (`on`, `off`, `powering_on`, …, lowercase). `?force=true` checks now. |
| `GET /jobs`, `/jobs/:id`, `/servers/:id/jobs` | One "job" per operation, with `steps`. Job status: `waiting` (scheduled), `pending`, `running`, `completed`, `failed`. |
| `DELETE /jobs/:id`, `POST /jobs/:id/run-now`, `POST /jobs/:id/retry` | cancel, run-now, retry |
| `POST /jobs/schedule` `{"server_id", "action", "scheduled_time"}` | A scheduled operation. |

The OS endpoints are available under both versions.
