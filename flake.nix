{
  description = "Acme-inspired TUI Text Editor";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
  };

  outputs =
    inputs:
    inputs.flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];

      imports = [ inputs.flake-parts.flakeModules.easyOverlay ];

      perSystem =
        { config, pkgs, ... }:
        {
          packages = rec {
            peak =
              with pkgs;
              buildGoModule {
                name = "peak";

                src = lib.cleanSource ./.;

                vendorHash = "sha256-4LZZqdtVB8CncThKcnoo1HBd4CKwuj0FVC8JSux9X78=";

                env.CGO_ENABLED = 0;

                ldflags = [
                  "-s"
                  "-w"
                ];

                doCheck = false; # breaks regexp, test data missing
              };
            default = peak;
          };

          overlayAttrs = {
            inherit (config.packages) peak;
          };
        };
    };
}
