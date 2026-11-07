{
  description = "digital-exit — Instagram export archive, media fetch, contacts, offline viewer + deletion tooling";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        # Build the CLI. The Go sources live under cli/ with its own go.mod.
        # It is intentionally stdlib-only (CGO_ENABLED=0) and has no external
        # Go module dependencies, so vendorHash can be null.
        digital-exit = pkgs.buildGoModule {
          pname = "digital-exit";
          version = "0.1.0";
          src = ./.;
          modRoot = "cli";
          subPackages = [ "." ];
          vendorHash = null;
          ldflags = [ "-s" "-w" ];
          env = {
            CGO_ENABLED = "0";
          };
          meta = {
            description = "Archive Instagram exports, fetch saved media (yt-dlp), classify contacts, emit offline viewer, and support account deletion workflow.";
            mainProgram = "digital-exit";
            license = pkgs.lib.licenses.mit;
            platforms = pkgs.lib.platforms.unix;
          };
        };
      in {
        packages.default = digital-exit;

        # `nix develop` — enter the dev shell with every tool the pipeline needs.
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go            # Go CLI toolchain
            nodejs_24     # (optional) for viewer/gallery validation scripts
            pnpm
            yt-dlp        # saved-post media download
            ffmpeg        # media merge for yt-dlp
            zip unzip file
          ];
          shellHook = ''
            echo "digital-exit dev shell ready."
            echo "  go:     $(go version)"
            echo "  yt-dlp: $(yt-dlp --version 2>/dev/null | head -1 || echo 'not in PATH')"
          '';
        };
      });
} 
