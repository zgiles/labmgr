# Deploying Lab Manager

Lab Manager is a single static binary with the web UI built in. It needs a config
file, a writable directory for its database, and network access to your BMCs and PDUs.

## Get the binary

**Release build:** download `labmgr-<version>-linux-amd64` (or `-arm64`) from the
[releases page](https://github.com/zgiles/labmgr/releases) and check it against
`SHA256SUMS`.

**From source** (Go 1.26+, Node.js 20.19+ or 22.12+, make):

```bash
git clone https://github.com/zgiles/labmgr.git
cd labmgr
make release          # → releases/labmgr-<commit>-linux-amd64
```

**With Nix:** `nix build github:zgiles/labmgr`.

## Network requirements

| From | To | Port | For |
|---|---|---|---|
| Lab Manager | each BMC | 443/tcp | Redfish: power, status, boot device, logs, power draw |
| Lab Manager | each BMC | 22/tcp | Serial console (SSH to the BMC CLI) |
| Lab Manager | Lenovo XCC | 3900/tcp | Graphical console relay (configurable per server) |
| Lab Manager | each PDU | 161/udp | SNMP (APC); HTTP PDUs use 80/tcp |
| Browsers | Lab Manager | 8098/tcp | Web UI and API (WebSockets and Server-Sent Events included) |
| Network-booting servers | Lab Manager | 8098/tcp | iPXE scripts (`/boot.ipxe`, `/api/v1/autoexec.ipxe`) |

## Configure

```bash
cp config/servers.yaml.example config/servers.yaml
chmod 600 config/servers.yaml      # it holds BMC and PDU credentials
$EDITOR config/servers.yaml
```

Every option is documented in the example and in the [configuration reference](configuration.md).
Lab Manager validates the file at start and reloads it whenever it changes (or on `SIGHUP`);
a bad edit is rejected and the previous config stays active.

## Run

```bash
./labmgr -config config/servers.yaml
```

| Flag | Default | |
|---|---|---|
| `-config` | `config/servers.yaml` | Servers, PDUs, timings, console recording |
| `-listen` | `0.0.0.0:8098` | Address to serve on |
| `-data` | next to the config (`servers.db`) | Operations, activity, power history |
| `-os-data` | next to the config (`servers.os.json`) | Boot images and assignments |
| `-debug` | off | Log every request and driver detail |

Console recordings go in `console/` next to the database. Back up the database,
`servers.os.json`, and your config; everything else is rebuilt from the hardware.

## systemd

A hardened unit is in [`deploy/labmgr.service`](../deploy/labmgr.service). It runs as a
dedicated `labmgr` user, restarts on failure, and can only write `/opt/labmgr/config`.

```bash
sudo useradd --system --home /opt/labmgr --shell /usr/sbin/nologin labmgr
sudo mkdir -p /opt/labmgr/config
sudo install -m 0755 labmgr-<version>-linux-amd64 /opt/labmgr/labmgr
sudo install -m 0600 config/servers.yaml /opt/labmgr/config/
sudo chown -R labmgr:labmgr /opt/labmgr
sudo cp deploy/labmgr.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now labmgr
journalctl -u labmgr -f
```

## NixOS

The repository is a flake with a package and a module:

```nix
{
  inputs.labmgr.url = "github:zgiles/labmgr";

  outputs = { nixpkgs, labmgr, ... }: {
    nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        labmgr.nixosModules.default
        {
          services.labmgr = {
            enable = true;
            # A string path, so the credentials never enter the Nix store.
            # Root-only permissions are fine; systemd hands it to the service.
            configFile = "/etc/labmgr/servers.yaml";
            # listen = "0.0.0.0:8098";
            # openFirewall = true;   # see Security below
          };
        }
      ];
    };
  };
}
```

The service runs as a dynamic user with its state in `/var/lib/labmgr`. It reads the
config at start, so restart it after editing.

## Upgrading

Operations running when Lab Manager stops are marked interrupted (never resumed), so
upgrade when nothing important is in flight (`GET /api/v2/operations?active=true`).

Replace the binary with `install` or `mv`, which swap the file, never `cp` over it:
`cp` rewrites the file in place and crashes the running process. If the binary lives on a
shared filesystem (NFS) that other hosts run it from, give each version a new file name
(`make release` does) rather than replacing one.

## Behind a reverse proxy

Live updates use Server-Sent Events and the consoles use WebSockets, so the proxy must
not buffer and must pass upgrades through. For nginx:

```nginx
location / {
    proxy_pass http://127.0.0.1:8098;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;   # map $http_upgrade → "upgrade"/"close"
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;          # Server-Sent Events
    proxy_read_timeout 1h;        # long-lived console sessions
}
```

`X-Forwarded-Proto: https` matters for the Lenovo graphical console, which adapts its
viewer to the page's scheme.

## Security

- **There is no login.** Anyone who can reach port 8098 can power servers off, change what
  they boot, and type on their consoles. Run it on a trusted management network, or put
  it behind a proxy that authenticates (OAuth2 Proxy, Authelia, nginx `auth_request`, …).
  Every change is recorded with the requester's IP and any `X-Labmgr-User` header.
- **Credentials stay on the server.** The API never returns BMC passwords or SNMP
  communities, and the graphical consoles use one-time logins issued by the BMC. Keep the
  config file `0600`.
- **BMC certificates aren't verified.** BMCs ship self-signed certificates and regenerate
  SSH host keys on reset, so Lab Manager accepts them as presented; it relies on the
  management network being trusted.
- **iPXE scripts are served without authentication**, as network booting requires. Don't put
  secrets in boot templates.
