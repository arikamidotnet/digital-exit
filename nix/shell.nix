# Non-flake fallback. Use with:  nix develop ./nix/shell.nix   (or nix-shell ./nix/shell.nix)
let
  nixpkgs = fetchTarball "https://github/NixOS/nixpkgs/archive/nixos-unstable.tar.gz";
  pkgs = import nixpkgs {};
in
pkgs.mkShell {
  packages = with pkgs; [
    go
    nodejs_24
    pnpm
    yt-dlp
    ffmpeg
    zip
    unzip
    file
  ];
}
