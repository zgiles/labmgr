# Changelog

## 2.0.0

A rewrite of the engine and the interface, with consoles, network-boot workflows, and
power monitoring added.

### Power control
- Operations replace the old job queue: each power on, power off, restart, graceful
  shutdown, or power cycle is planned from the server's current state into visible steps
  (outlets → wait for the BMC → power on → wait until it reports on), run one at a time per
  server, retried with backoff, and cancellable mid-sequence.
- Steps wait on the hardware (the BMC answering, the system reporting on or off) instead of
  fixed delays.
- Scheduled operations are planned when they run. Operations and history survive restarts;
  anything mid-flight when Lab Manager stops is marked interrupted, never resumed blindly.
- Fixed: reboot left servers powered off; scheduled actions never ran; multi-outlet PDU
  schedules failed.

### Consoles
- Serial console in the browser over the BMC's SSH CLI (iDRAC, XCC, iLO), shared between
  viewers, with recent output kept and downloadable.
- Console recording after power on, restart, power cycle, and reinstall (configurable;
  continuous recording optional), linked from each operation.
- Graphical console with no BMC login: one-time iDRAC9 virtual console logins, and a hosted
  Lenovo XCC viewer relayed through Lab Manager.

### Network boot
- Boot images assignable per server, "next boot only" assignments, and a one-click
  reinstall (serve an installer once, PXE once, restart, wait for the fetch).
- BMC boot device control (PXE, BIOS setup, disk, once or always) and a preview of each
  server's iPXE script. MACs match in any case or separator format.
- `/boot.ipxe` bootstrap served by the binary with an absolute chain URL.

### Monitoring
- Power draw from each BMC every 10 seconds, kept 7 days, with sparklines and charts.
- Hardware details, health, and the BMC event log (iDRAC and XCC).

### Interface and API
- New web UI (Vue 3, TypeScript): live updates over Server-Sent Events, a server table with
  live operation progress, and a per-server panel for overview, boot, activity, and
  consoles. Light and dark themes; works on a phone.
- `/api/v2`: operations with `?wait=` for one-call results, servers addressable by name, a
  plain-text overview (`?format=text`), activity history, and a live event stream.
  `/api/v1` keeps working.
- The API no longer returns BMC passwords or SNMP communities.

### Running it
- Config reloads automatically when the file changes; graceful shutdown.
- systemd unit (`deploy/labmgr.service`), and a Nix flake with a NixOS module
  (`services.labmgr`).
- `-demo` runs a simulated lab, to try Lab Manager without hardware.
- Documentation under `docs/`: a user guide, configuration, network boot, deployment, and
  the API.
- Requires Go 1.26 to build.

## 1.0.0

The original Lab Manager: IPMI/Redfish and PDU power control with a job queue, status
polling, iPXE boot scripts, and a Vue web UI.
