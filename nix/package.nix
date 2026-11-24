{
  lib,
  buildGoModule,
  buildNpmPackage,
  nodejs_22,
}:

let
  version = "2.0.0";

  # The web UI, built once and embedded into the Go binary
  frontend = buildNpmPackage {
    pname = "labmgr-frontend";
    inherit version;
    src = lib.fileset.toSource {
      root = ../frontend;
      fileset = lib.fileset.unions [
        ../frontend/package.json
        ../frontend/package-lock.json
        ../frontend/index.html
        ../frontend/tsconfig.json
        ../frontend/vite.config.ts
        ../frontend/src
      ];
    };
    nodejs = nodejs_22;
    npmDepsHash = "sha256-+2s+lVchvQ2UYmkWBKw+IqkLSBhpXVaNx+kBm9uqL4Y=";
    installPhase = ''
      runHook preInstall
      cp -r dist $out
      runHook postInstall
    '';
  };
in
buildGoModule {
  pname = "labmgr";
  inherit version;

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      (lib.fileset.difference ../backend (lib.fileset.maybeMissing ../backend/static))
      # The tests check that the shipped example configs still parse
      ../config/servers.yaml.example
    ];
  };
  modRoot = "backend";
  vendorHash = "sha256-ywlFmop08nRHq9WxOL1Flntd3CZFONstUOBnMzkAD+A=";

  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
  ];

  preBuild = ''
    rm -rf static
    cp -r ${frontend} static
    chmod -R u+w static
  '';

  # The tests drive a simulated lab; nothing touches real hardware
  doCheck = true;

  passthru = { inherit frontend; };

  meta = {
    description = "Power, network boot, and console control for lab servers through their BMCs and PDUs";
    license = lib.licenses.mit;
    mainProgram = "labmgr";
    platforms = lib.platforms.linux;
  };
}
