# Building Lab Manager

Lab Manager builds into a single binary with the web UI inside it. There are three ways to
get one:

| | You need | You get |
|---|---|---|
| [Download a release](#download-a-release) | Nothing | A Linux binary (amd64 or arm64) |
| [Build with Nix](#build-with-nix) | Nix with flakes enabled | `result/bin/labmgr` |
| [Build with make](#build-with-make) | Go 1.26+, Node.js 20.19+ or 22.12+, make | `releases/labmgr-<version>-linux-amd64` |

## Download a release

Each tagged version has binaries on the
[releases page](https://github.com/zgiles/labmgr/releases):

```bash
curl -LO https://github.com/zgiles/labmgr/releases/download/v2.0.0/labmgr-v2.0.0-linux-amd64
curl -LO https://github.com/zgiles/labmgr/releases/download/v2.0.0/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
chmod +x labmgr-v2.0.0-linux-amd64
```

## Build with Nix

The repository is a Nix flake. Nix fetches the right Go and Node.js itself, so you don't
need either installed.

```bash
git clone https://github.com/zgiles/labmgr.git
cd labmgr
nix build                                         # → ./result/bin/labmgr
./result/bin/labmgr -config config/servers.yaml
```

Or build and run in one step, or straight from GitHub without cloning:

```bash
nix run . -- -config config/servers.yaml
nix run github:zgiles/labmgr -- -demo
nix run github:zgiles/labmgr/v2.0.0 -- -config /etc/labmgr/servers.yaml   # a specific version
```

Things to know:
- **Tests run during the build.** The Go tests run as part of `nix build`, so a build that
  succeeds has passed them.
- **Every build is kept separately.** It lands in its own read-only path under
  `/nix/store`, and `result` points at the latest. Rebuilding never touches a binary that's
  already running, so it's safe even on a shared filesystem.
- **New files must be in git.** A flake only sees files git knows about, so `git add` any
  new file before building. (Uncommitted changes to tracked files are included.)
- **Dependency hashes.** After you change `backend/go.mod` or
  `frontend/package-lock.json`, the build stops with a hash mismatch and prints the
  correct hash. Put it in `nix/package.nix` (`vendorHash` for Go, `npmDepsHash` for
  Node.js) and build again.

To run Lab Manager as a service on NixOS, use the flake's module instead; see
[Deployment](deployment.md#nixos).

## Build with make

Install Go 1.26 or newer, Node.js 20.19+ or 22.12+, make, and git. With Nix, `nix develop`
gives you a shell with all of them. You can also run one command with them, e.g.
`nix develop -c make release`.

```bash
git clone https://github.com/zgiles/labmgr.git
cd labmgr
make release      # → releases/labmgr-<version>-linux-amd64
```

| Target | Does |
|---|---|
| `make release` | Builds the web UI and a static Linux amd64 binary into `releases/`, named after the current commit (`git describe`, e.g. `labmgr-v2.0.0-1-g9721798-linux-amd64`). It never overwrites an existing file; if one with that name exists, it stops. |
| `make build` | The same build into `./labmgr`, replacing it. Fine for trying things locally. |
| `make test` | The Go tests (with the race detector, against simulated hardware) and the TypeScript typecheck. |

For an arm64 binary, download the release, or build with Nix on an arm64 machine.

## Replacing a running binary

Never overwrite the binary a running Lab Manager is using, for example by running
`make build` again or using `cp` over it. Rewriting an executable in place can crash the
running process, and on a shared filesystem (NFS) it crashes processes on other hosts too.

Build to a new file instead, then restart onto it. `make release` and `nix build` both do
this automatically. With a downloaded binary, use `install` or `mv`, which swap in a new
file rather than rewriting the old one. To restart cleanly, see
[Upgrading](deployment.md#upgrading).
