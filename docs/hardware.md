# Hardware and protocols

How Lab Manager talks to each kind of device, and what works on which BMC.

## What's used for what

| Talks to | Protocol | Port | Used for |
|---|---|---|---|
| BMC | **Redfish** (HTTPS, JSON) | 443/tcp | Power state and power actions, the boot device, hardware details and health, event logs, power draw, and one-time console logins |
| BMC | **SSH** to the BMC's command line | 22/tcp | The serial console: Lab Manager logs in and runs the attach command (`console com2`, `console 1`, …) |
| Lenovo XCC | WebSocket (remote presence) | 3900/tcp | The hosted graphical console, relayed through Lab Manager |
| APC PDU | **SNMP** v1/v2c | 161/udp | Reading and switching outlets |
| Other PDUs | HTTP | 80/tcp | Switching outlets (no state readback) |
| Servers → Lab Manager | HTTP | 8098/tcp | iPXE boot scripts |
| Browsers and scripts → Lab Manager | HTTP, Server-Sent Events, WebSocket | 8098/tcp | The UI, the API, live updates, consoles |

### Not IPMI

Lab Manager doesn't use the IPMI protocol at all, and never has. That rules out RMCP+
on 623/udp, `ipmitool`, and serial-over-LAN through IPMI. It talks to BMCs over Redfish,
the HTTPS API that iDRAC, XCC, iLO, and most current BMCs provide. The name survives in
two places, where it just means "the BMC":
- the `ipmi:` key in the config
- the v1 API's `/servers/:id/ipmi` endpoint

Redfish works the same way across vendors and gives far more than IPMI: health, firmware
versions, event logs, power readings, and boot settings, all over one HTTPS connection.
The serial console goes over SSH to the BMC rather than IPMI serial-over-LAN. That's more
reliable, and it uses the same credentials.

### Redfish sessions

Lab Manager logs in to each BMC once and reuses that session for every request. It only
logs in again when the BMC drops the session: after a BMC restart, or after the session
was idle for 5 minutes and Lab Manager logged out. It also logs out when it shuts down.

Some BMCs check a password slowly. An iDRAC8 takes 4–5 seconds per request with the
password, and under a second with a session. One shared session also uses only one of
the BMC's session slots, however often Lab Manager polls. A BMC that doesn't offer
sessions is sent the password with each request instead.

### How often

| | Every | |
|---|---|---|
| BMC status (power state) | 30 s | 5 s while an operation runs on the server; 90 s while unreachable |
| Power draw | 10 s | Only while the BMC answers |
| Hardware details | 1 h | |
| PDU outlets | 30 s | One SNMP query per PDU covers all its outlets |
| Event log | on demand | When someone opens it |

## BMC support

| | Dell iDRAC9 | Dell iDRAC8 | Lenovo XCC | Other Redfish BMCs |
|---|---|---|---|---|
| Power state, power actions | ✓ | ✓ ¹ | ✓ | ✓ |
| Power draw | ✓ | ✓ | ✓ | If the BMC reports it |
| Hardware details, health | ✓ | ✓ | ✓ | ✓ |
| Event log | ✓ | ✓ ² | ✓ | Usually |
| Boot device (PXE, BIOS, disk) | ✓ | ✓ ¹ | ✓ | ✓ |
| Serial console | ✓ `console com2` | ✓ ¹ `console com2` | ✓ `console 1` | Set `console.command` |
| Graphical console without a login | ✓ ³ | — opens the iDRAC login | ✓ hosted viewer | — opens the BMC's web UI |

1. Standard Redfish that iDRAC8 supports from firmware 2.40 on. So far it has only been
   tested read-only on an iDRAC8.
2. iDRAC8 returns its whole Lifecycle log at once, so opening the event log can take
   15–20 seconds.
3. Needs an iDRAC9 Enterprise or Datacenter license.

Tested on PowerEdge R640 and R740xd (iDRAC9), PowerEdge R630 (iDRAC8 2.86), and
ThinkSystem SR630 (XCC).

**PDUs:** APC switched PDUs are supported over SNMP, through the PowerNet MIB's
`sPDUOutletCtl` outlet control, which the AP79xx rack PDUs support. The HTTP type covers
PDUs with a simple `/outlet?N=…` web interface.

## Network caveats

**A host can't manage its own BMC over a shared port.** Many servers can run the BMC
over one of the host's own network ports (Dell calls this "Shared LOM"). Traffic from
that host to its BMC never arrives: it leaves through the same port, and the switch won't
send it back the way it came. The same goes for VMs on that host. If Lab Manager runs on
the server it manages, or in a VM on it, give that BMC its dedicated port, or run Lab
Manager somewhere else. The symptom is that BMC showing **Unreachable** ("no route to
host") in Lab Manager while other machines reach it fine.

Don't power off the machine Lab Manager runs on from Lab Manager, either: the operation
stops partway, along with Lab Manager.
