# NixOS module: services.labmgr
self:
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.labmgr;
  port = lib.toInt (lib.last (lib.splitString ":" cfg.listen));
in
{
  options.services.labmgr = {
    enable = lib.mkEnableOption "Lab Manager (BMC/PDU power, network boot, consoles)";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "labmgr.packages.\${system}.default";
      description = "The Lab Manager package.";
    };

    configFile = lib.mkOption {
      type = lib.types.str;
      example = "/run/secrets/labmgr/servers.yaml";
      description = ''
        Path to servers.yaml. It holds BMC and PDU credentials, so give a path as a
        string (not a Nix path), so it stays out of the world-readable store; root-only
        permissions are fine, systemd hands it to the service. The service reads it at
        start: restart it after editing.
      '';
    };

    listen = lib.mkOption {
      type = lib.types.str;
      default = "0.0.0.0:8098";
      description = "Address and port to serve the web UI, API, and iPXE scripts on.";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Open the listen port in the firewall. The API has no login; only do this on a trusted network.";
    };

    extraArgs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "-debug" ];
      description = "Extra command-line flags.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.labmgr = {
      description = "Lab Manager";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];

      serviceConfig = {
        ExecStart = lib.escapeShellArgs (
          [
            (lib.getExe cfg.package)
            "-config"
            "%d/servers.yaml"
            "-listen"
            cfg.listen
            "-data"
            "/var/lib/labmgr/servers.db"
            "-os-data"
            "/var/lib/labmgr/servers.os.json"
          ]
          ++ cfg.extraArgs
        );
        LoadCredential = [ "servers.yaml:${cfg.configFile}" ];
        DynamicUser = true;
        StateDirectory = "labmgr";
        WorkingDirectory = "/var/lib/labmgr";
        Restart = "on-failure";
        RestartSec = "5s";
        TimeoutStopSec = "15s";

        # It needs the network (BMCs, PDUs, browsers) and its state directory, nothing else
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
        ];
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" ];
        CapabilityBoundingSet = "";
        UMask = "0077";
      };
    };

    networking.firewall.allowedTCPPorts = lib.mkIf cfg.openFirewall [ port ];
  };
}
