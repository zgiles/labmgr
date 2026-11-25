# Lab Manager documentation

- **[User guide](guide.md)**: the servers list and server states, power operations and
  scheduling, network boot and reinstalls, consoles and recordings, power monitoring, and
  activity.
- **[Configuration](configuration.md)**: `servers.yaml`, covering servers, BMCs, PDUs,
  consoles, console recording, timings, and command-line flags.
- **[Hardware and protocols](hardware.md)**: what Lab Manager uses to talk to BMCs and
  PDUs (Redfish, SSH, SNMP; not IPMI), and what works on each BMC.
- **[Network boot](network-boot.md)**: pointing iPXE at Lab Manager, boot images, and
  templates.
- **[Deployment](deployment.md)**: release binaries, systemd, NixOS, network ports,
  reverse proxies, upgrades, and security.
- **[API](api.md)**: the HTTP API for scripts and agents.
- **[Development](development.md)**: building, testing, and how the code is laid out.

New to Lab Manager? Run `labmgr -demo` and open http://localhost:8098. You get a simulated
lab to click through, and nothing real is touched.
