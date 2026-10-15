---
name: lab-manager
description: Control the lab's physical servers through the Lab Manager REST API — power on/off/restart/power-cycle via the BMC, control PDU outlets, check status and power draw, schedule actions, set the next boot device, reinstall via network boot, read BMC event logs, and assign PXE/OS boot images. Use whenever the user asks to power, reboot, check, schedule, boot-configure, or reinstall a lab server (e.g. "power on node01", "reboot the compute nodes", "is node02 up?", "schedule node03 off tonight", "how much power is node02 drawing?", "why is node02 unhealthy?").
---

# Lab Manager

Lab Manager is a Go service that controls the lab's physical servers through their
BMCs (Redfish) and a networked PDU. Everything that changes a server is an
**operation**: a list of steps planned from the server's current state, run one at
a time per server. It also handles PXE/OS boot images and records power draw.

## Base URL

Lab Manager's address comes from `LABMGR_URL` (e.g. `http://labmgr.example:8098`); if it
isn't set, ask the user where Lab Manager runs. Set once per session and reuse:

```bash
BASE=${LABMGR_URL:?set LABMGR_URL to Lab Manager's address}/api/v2
H=(-H 'Content-Type: application/json' -H 'X-Labmgr-User: claude')
```

Check it's up: `curl -sf "$BASE/info" >/dev/null && echo up || echo down`. If it's down,
tell the user; don't start it yourself, since it talks to real hardware.

The web UI is the same address; link the user to a server with
`$LABMGR_URL/#/servers/<id>` (add `/boot`, `/activity`, or `/console`).

## Safety rules — read before acting

These commands act on **real physical machines**. Treat them like production.

- **Confirm before anything disruptive**: `power_off`, `restart`, `graceful_restart`,
  `graceful_shutdown`, `power_cycle`, `reinstall`, BMC `off`/`reboot`/`nmi`, outlet
  `off`. State which server(s) and which action, and wait for an explicit go-ahead.
  Power on, status checks, power draw, reading logs, and setting a one-time boot
  device don't need confirmation.
- **Resolve names to IDs first.** Never guess a server ID — list servers and match
  what the user said to a real `id`. If the match is ambiguous, ask.
- **One server unless told otherwise.** For "the compute nodes" or "all servers",
  list them back and confirm the set before fanning out.
- **Operations are async.** A submit returns immediately; poll the operation until
  it finishes and report the result. Don't assume success.
- Never print IPMI/PDU passwords (the API doesn't return them; don't read them from
  the config file either).

## Servers: ask the API

The API is the inventory: every server, its state, power draw, hardware, boot
image, and what last happened to it. Start here:

```bash
curl -s "$BASE/servers?format=text"
```

```
ID      NAME    STATE                                           WATTS  HARDWARE                          NETWORK BOOT  ACTIVITY
node01  node01  unreachable (no route to host (is it plugged…))  -      -                                 local disk    Power off node01 succeeded 5d ago
node02  node02  on                                              531    Dell Inc. PowerEdge R740 (OK)     local disk    -
```

(If that prints JSON instead of a table, Lab Manager is an older build; use
`curl -s "$BASE/servers" | jq -r '.[] | "\(.id)\t\(.state.state)\t\(.state.watts // "-") W\t\(.description)"'`.)

Paths take a server's ID, name, or hostname, in any case (`/servers/node01`,
`/servers/NODE01`). An unknown name returns `404` with the list of real IDs — use
it to correct yourself, and ask the user if the match isn't obvious.

```bash
curl -s "$BASE/servers/<id>" | jq '{state: .state.state, watts: .state.watts, hardware: .state.hardware, last: .last_operation}'
curl -s -X POST "$BASE/servers/<id>/refresh" | jq '.state'   # check now instead of using the last poll
```

## Context the API doesn't have

The API is the inventory; don't keep a copy of it here. If your lab has facts the API
can't know (CPU generations, which nodes are diskless, how to ssh in), add them to your
own copy of this skill (e.g. `~/.claude/skills/lab-manager/SKILL.md`) for the servers that
need them.

## State values

`state.state` values: `on`, `off`, `powered` (outlets on but the BMC isn't
responding), `partial`, `unreachable`, `unknown` (not checked yet), and while an
operation runs: `powering_on`, `powering_off`, `shutting_down`, `restarting`,
`power_cycling`, `busy` (`state.operation` says which step). `state.bmc_error` /
`state.pdu_error` explain unreachable. `state.watts` is the latest power draw.

## Operations

Add `?wait=300` and the response comes back when the operation has finished, with
the outcome — one call, no polling loop:

```bash
curl -s -X POST "$BASE/servers/<id>/operations?wait=300" "${H[@]}" -d '{"kind":"power_on"}' \
  | jq '{id, label, status, error, note, steps: [.steps[] | {label, status, attempts, error}]}'
```

| kind | extra fields | confirm first? |
|---|---|---|
| `power_on` | | no |
| `graceful_shutdown` (ask the OS to shut down) | | yes |
| `power_off` (force off, then cut outlets) | | yes |
| `restart` (hard reset through the BMC) | | yes |
| `graceful_restart` (ask the OS to reboot) | | yes |
| `power_cycle` (fully off incl. outlets, then on) | | yes |
| `boot_override` | `"target": "pxe" \| "bios" \| "disk" \| "none"` | no |
| `reinstall` | `"target": "<boot image id>"` | yes |
| `bmc` (one raw BMC action) | `"action": "on" \| "off" \| "reboot" \| "nmi" \| ...` | yes unless `on` |
| `outlet` (one PDU outlet) | `"target": "pdu-01:8", "action": "on" \| "off"` | yes for `off` |

A `409` means the server is busy or there's nothing to do (e.g. "node01 is
already on"); report that to the user rather than retrying.

If `status` is still `running` (the wait ran out, or Lab Manager is an older build that
ignores `?wait`), keep waiting on it:

```bash
until curl -s "$BASE/operations/<op-id>?wait=120" | jq -e '.status | IN("succeeded","failed","cancelled","interrupted")' >/dev/null; do sleep 3; done
curl -s "$BASE/operations/<op-id>" | jq '{status, error, note, steps: [.steps[] | {label, status, attempts, error}]}'
```

Power on can take several minutes when outlets are involved (outlets → BMC boot →
power on → wait until the system reports on). A `succeeded` power on means the BMC
reports the system on — the OS may still be booting; wait for ssh before using a node.

Other operation actions: `POST $BASE/operations/<id>/cancel`, `/run-now`
(scheduled only), `/retry` (failed/cancelled/interrupted).
List: `curl -s "$BASE/operations?server=<id>&limit=10"`, or `?active=true`.

### Schedule for later

`scheduled_for` is RFC3339 (UTC). The steps are planned when it runs.

```bash
curl -s -X POST "$BASE/servers/<id>/operations" "${H[@]}" \
  -d '{"kind":"graceful_shutdown","scheduled_for":"2026-10-28T03:00:00Z"}' | jq '{id, label, scheduled_for}'
curl -s "$BASE/operations?active=true" | jq -r '.[] | "\(.id)\t\(.status)\t\(.label)\t\(.scheduled_for // "")"'
```

## Power draw

Read from each responding BMC every 10 seconds; 7 days kept.

```bash
curl -s "$BASE/servers/<id>" | jq '.state.watts'                                   # now
curl -s "$BASE/servers/<id>/power?hours=1" | jq '[.samples[][1]] | {min: min, max: max, avg: (add/length)}'
curl -s "$BASE/power?hours=1" | jq 'map_values(.[-1][1])'                          # latest for every server
```

`samples` are `[unix_ms, watts]`, oldest first; ranges over a few hours are averaged down.

## Diagnostics

```bash
curl -s "$BASE/servers/<id>/bmc" | jq '.info'                          # model, serial, BIOS, health
curl -s "$BASE/servers/<id>/bmc/logs?limit=30" | jq -r '.[] | "\(.created)  \(.severity)  \(.message)"'
curl -s "$BASE/servers/<id>/events" | jq -r '.[] | "\(.at)  \(.message)"'  # Lab Manager's own history
curl -s "$BASE/servers/<id>/console/log"                                 # recent serial console output
```

For an interactive console, send the user to the web UI's Console tab
(`$LABMGR_URL/#/servers/<id>/console`): serial console there, and a
"Graphical console" button that opens the BMC KVM (straight in on Dell iDRAC).

## Network boot (PXE)

```bash
curl -s "$BASE/os/templates" | jq -r '.templates[] | "\(.id)\t\(.boot_method)\t\(.name)"'
curl -s "$BASE/servers/<id>/boot-script" | jq -r '.source_name, .script'   # what it gets right now
curl -s -X PUT "$BASE/servers/<id>/os" "${H[@]}" -d '{"template_id":"<tmpl>"}'                  # every network boot
curl -s -X PUT "$BASE/servers/<id>/os" "${H[@]}" -d '{"template_id":"<tmpl>","one_shot":true}'  # next network boot only
curl -s -X DELETE "$BASE/servers/<id>/os"                                                        # back to local disk
curl -s -X POST "$BASE/os/bulk-assign" "${H[@]}" -d '{"server_ids":["node01","node02"],"template_id":"<tmpl>"}'
curl -s "$BASE/os/assignments" | jq
```

An assignment only matters when the server network boots. To actually reinstall,
use the `reinstall` operation (confirm first): it handles the one-time assignment,
the BMC boot device, and the restart, and reports when the server fetched its script.

## Reporting back

Say concretely what happened: the server, the operation and its ID, the steps that
ran, and the final state. If it failed, quote the failed step's `error`.

## Older scripts (v1)

`/api/v1/...` (jobs, `/servers/<id>/power`, `/reboot`, `/ipmi`, `/pdu/...`) still works
for existing scripts, but use v2 for anything new.
