{
  description = "Acme-inspired TUI Text Editor";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    systems.url = "github:nix-systems/default";
  };

  outputs =
    inputs:
    inputs.flake-parts.lib.mkFlake { inherit inputs; } {
      systems = import inputs.systems;

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

                vendorHash = "sha256-JsmeQX9l1rvsBOdp/QRId0y2rnpqQiiwP1RZtNWl+ww=";

                env.CGO_ENABLED = 0;

                ldflags = [
                  "-s"
                ];
              };
            default = peak;
          };

          overlayAttrs = {
            inherit (config.packages) peak;
          };

          devShells.default = pkgs.mkShellNoCC {
            env.CGO_ENABLED = 0;
            packages = with pkgs; [
              go
              gopls
            ];
          };
        };
    };
}
