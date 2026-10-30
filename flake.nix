{
  description = "Lab Manager: power, network boot, and console control for lab servers through their BMCs and PDUs";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: rec {
        labmgr = pkgs.callPackage ./nix/package.nix { };
        default = labmgr;
      });

      nixosModules.default = import ./nix/module.nix self;
      nixosModules.labmgr = self.nixosModules.default;

      # `nix flake check` builds the package (running the Go tests) and renders the
      # module's systemd unit for a minimal system that enables it
      checks = forAllSystems (
        pkgs:
        let
          system = pkgs.stdenv.hostPlatform.system;
          example = nixpkgs.lib.nixosSystem {
            inherit system;
            modules = [
              self.nixosModules.default
              {
                services.labmgr = {
                  enable = true;
                  configFile = "/run/secrets/labmgr/servers.yaml";
                };
                boot.loader.grub.enable = false;
                fileSystems."/" = {
                  device = "/dev/null";
                  fsType = "ext4";
                };
                system.stateVersion = "25.11";
              }
            ];
          };
        in
        {
          package = self.packages.${system}.default;
          module = pkgs.writeText "labmgr.service" example.config.systemd.units."labmgr.service".text;
        }
      );

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.nodejs_22
            pkgs.gnumake
          ];
        };
      });
    };
}
