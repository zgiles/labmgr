# Network boot

Lab Manager serves iPXE scripts. When a server network boots, its iPXE asks Lab Manager
for a script, and Lab Manager finds the server by MAC address and answers with whatever
that server should boot. You choose what that is in the UI (the **Boot** tab and the
**Boot images** page) or through the [API](api.md#network-boot).

## Point iPXE at Lab Manager

Have your DHCP server send iPXE clients to:

```
http://<lab-manager>:8098/boot.ipxe
```

That's a small bootstrap that chains to `/api/v1/autoexec.ipxe?mac=${net0/mac}` with an
absolute URL. You can also chain to that URL directly from your own iPXE script.

With ISC dhcpd, for example:

```
if exists user-class and option user-class = "iPXE" {
  filename "http://192.0.2.10:8098/boot.ipxe";
} else {
  filename "ipxe.efi";          # or undionly.kpxe for BIOS; served by your TFTP server
}
```

dnsmasq:

```
dhcp-match=set:ipxe,175
dhcp-boot=tag:ipxe,http://192.0.2.10:8098/boot.ipxe
```

Each server needs `metadata.mac` in the config. Lab Manager matches MACs in any case and
with any separator. The Boot tab shows when each server last fetched a script.

## Boot images

A boot image is one of:

- **Chain**: a URL for iPXE to chain to, e.g. `https://boot.netboot.xyz/menu.ipxe`.
- **Script**: an inline iPXE script, rendered per server (see [Templates](#templates)).
- **Local disk**: boot from the first disk (`sanboot --drive 0x80`), dropping to the iPXE
  shell if that fails.

Create and edit boot images on the **Boot images** page. Assign one to a server in its
**Boot** tab, for every network boot or for the next one only. A next-boot-only
assignment reverts once the server has fetched it. The **Reinstall** action uses this to
serve an installer exactly once.

## Templates

Inline scripts use Go template syntax, with these values:

| | |
|---|---|
| `{{.MAC}}` | The MAC the request came from. |
| `{{.Hostname}}`, `{{.Role}}` | `metadata.hostname` and `metadata.role`. |
| `{{.ServerName}}`, `{{.ServerID}}` | From the config. |
| `{{.IncomingServerIP}}` | The address the request arrived on, for building URLs back to Lab Manager. |
| `{{.Metadata.key}}` | Any metadata value. |

A Debian netboot install:

```
#!ipxe
set mirror http://deb.debian.org/debian/dists/stable/main/installer-amd64/current/images/netboot/debian-installer/amd64
kernel ${mirror}/linux auto=true priority=critical hostname={{.Hostname}} url=http://boot.lab.example/preseed/{{.Role}}.cfg
initrd ${mirror}/initrd.gz
boot
```

The **Boot** tab shows the exact script a server would get right now, rendered.

## Priority

For each request, Lab Manager answers with the first of:

1. the boot image assigned to the server (a next-boot-only assignment is cleared once
   it's served);
2. `metadata.next-path` in the config (a chain URL);
3. `metadata.boot-template` in the config (an inline script);
4. local disk.

A MAC that matches no server also gets local disk.

Options 2 and 3 are older, config-file ways of doing the same thing as boot images. They
still work:

```yaml
metadata:
  mac: aa:bb:cc:dd:ee:01
  next-path: https://boot.netboot.xyz/menu.ipxe
```

## The BMC boot device

Lab Manager only answers servers that network boot. Whether a server does is up to its
BIOS boot order, or the BMC's one-time boot override. The **Boot** tab sets the override:
**Network once**, **BIOS setup once**, **Disk once**, or **Clear**. Reinstall sets
**Network once** for you.

iPXE scripts are served without authentication, as network booting requires. Don't put
secrets in boot images.
