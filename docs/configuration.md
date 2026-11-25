# Configuration

Lab Manager reads one YAML file, `config/servers.yaml` by default. Start from
[`config/servers.yaml.example`](../config/servers.yaml.example). The file holds BMC and PDU
credentials, so keep it `chmod 600`.

Lab Manager checks the file at startup and reloads it whenever it changes (or on `SIGHUP`).
If an edit is invalid, the reload is rejected, the error is logged, and the previous config
stays active.

- [Servers](#servers)
- [PDUs](#pdus)
- [Console recording](#console-recording)
- [Timings](#timings)
- [Command-line flags](#command-line-flags)
- [Files Lab Manager writes](#files-lab-manager-writes)

## Servers

```yaml
servers:
  - id: node01                  # stable identifier, used in URLs and the API
    name: node01                # display name
    description: Compute, Dell R640
    ipmi:                       # the BMC, reached over Redfish (HTTPS) and SSH
      host: 192.0.2.11
      username: labmgr
      password: "..."
    pdu_outlets: [pdu-a:1, pdu-a:2]   # optional; "<pdu id>:<outlet number>"
    console:                    # optional
      command: console com2     # see below
      ssh_port: 22
      web_url: https://192.0.2.11      # link for the BMC's web UI
      kvm_port: 3900            # Lenovo XCC remote presence port
      capture: off              # "off" or "always"; overrides console_capture
    metadata:
      mac: aa:bb:cc:dd:ee:01    # needed for network boot; any case or separator
      hostname: node01.lab.example
      role: compute
```

A server needs a BMC (`ipmi.host`), PDU outlets, or both:

| Has | Power on | Power off | State comes from |
|---|---|---|---|
| BMC and outlets | outlets on → wait for the BMC → power on | power off → wait → outlets off | the BMC (the outlets explain a dark BMC) |
| BMC only | power on | power off | the BMC |
| Outlets only (switches, appliances) | outlets on | outlets off | the outlets |

The `ipmi` key is named for history. Lab Manager talks to BMCs over **Redfish**
(HTTPS) and SSH, not IPMI, using one login session per BMC (see
[Hardware and protocols](hardware.md)). Give it an account that can control power and use the console. On iDRAC that's
Operator or Administrator; on XCC, a role with Remote Console and Power/Restart.

**`console.command`** is typed at the BMC's SSH prompt to attach to the host's serial port.
When it's not set, it's picked by vendor: `console com2` (Dell iDRAC), `console 1` (Lenovo
XCC), `vsp` (HPE iLO).

**`metadata`** takes any keys you like. They're shown in the UI, searchable, and available
to boot templates. Lab Manager itself reads these keys:

| Key | Used for |
|---|---|
| `mac` | Matching iPXE requests to this server. |
| `hostname` | Display, boot templates, and finding the server by hostname in the API. |
| `role` | Display and boot templates. |
| `next-path`, `boot-template` | Config-level boot defaults; see [Network boot](network-boot.md#priority). |

## PDUs

```yaml
pdus:
  - id: pdu-a
    name: Rack A PDU
    host: 192.0.2.50
    type: apc                   # SNMP; any other type uses the HTTP interface
    snmp_community: private     # write community
    snmp_version: v2c           # v1 or v2c
    snmp_port: 161
  - id: pdu-b
    name: Old PDU
    host: 192.0.2.51
    type: http
    username: admin
    password: "..."
```

Lab Manager reads every outlet of an APC PDU with one SNMP query per poll, so many servers
on one PDU don't multiply the load.

## Console recording

```yaml
console_capture:                # optional; these are the defaults
  after: [power_on, restart, graceful_restart, power_cycle, reinstall]
  minutes: 15                   # record this long after the operation starts
  always: false                 # record every server continuously (one file per day)
  retain_days: 30               # delete recordings older than this
```

`after` takes operation kinds: `power_on`, `power_off`, `graceful_shutdown`, `restart`,
`graceful_restart`, `power_cycle`, `reinstall`. Use `after: []` to turn per-operation
recording off.

A server's `console.capture` overrides this. `off` never records that server, and
`always` records it continuously whatever the global setting.

Continuous recording holds the BMC's serial session open all the time. While it runs,
nothing else, such as `ipmitool sol` or the BMC's web console, can use the serial port. The
per-operation recordings let go when they finish.

## Timings

```yaml
delays:                         # seconds; 0 or unset means no pause
  pdu_to_power_on_seconds: 10   # after outlets come on, before waiting for the BMC
  power_off_to_pdu_seconds: 30  # after the system is off, before cutting outlets
  between_ipmi_seconds: 2       # power cycle pauses twice this (at least 5) between off and on
  between_pdu_seconds: 3        # between consecutive outlet switches (never less than 3)
```

These are pauses on top of the real waits. Steps like "wait for the BMC" and "wait until it
reports on" watch the hardware anyway, so the pauses only need to cover what Lab Manager
can't observe. Examples are inrush between outlets, or a BMC that answers before it can
power on the host.

## Command-line flags

| Flag | Default | |
|---|---|---|
| `-config` | `config/servers.yaml` | The config file. |
| `-listen` | `0.0.0.0:8098` | Address to serve the UI, the API, and iPXE scripts on. |
| `-data` | next to the config (`servers.db`) | Operations, activity, and power history. |
| `-os-data` | next to the config (`servers.os.json`) | Boot images and assignments. |
| `-demo` | off | Run a simulated lab instead of reading `-config`. Nothing real is touched or saved. |
| `-debug` | off | Log every request and driver detail. |

## Files Lab Manager writes

| Path | What | Back it up? |
|---|---|---|
| `servers.db` | Operations, activity history, and 7 days of power readings (bbolt). | Yes, if you want the history. |
| `servers.os.json` | Boot images and which server boots what. | Yes. |
| `console/<server>/` | Console recordings, pruned after `retain_days`. | Optional. |

All of these live next to the config unless `-data` or `-os-data` say otherwise. The
console directory always sits next to the database. Everything else, like server state and
hardware details, is read from the hardware again at startup.
