{
  description = "gguf-manager — local web UI for managing GGUF models with llama-server";

  inputs = {
    nixpkgs.url     = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    gomod2nix = {
      url   = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, flake-utils, gomod2nix }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        inherit (gomod2nix.legacyPackages.${system}) buildGoApplication;
      in {
        packages.default = buildGoApplication {
          pname   = "gguf-manager";
          version = "0.1.0";
          src     = ./.;
          modules = ./gomod2nix.toml;

          meta = {
            description = "Local web UI for managing GGUF models with llama-server";
            license     = pkgs.lib.licenses.mit;
            maintainers = [];
          };
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls
            gotools
            gomod2nix.packages.${system}.default
          ];
        };
      })
    // {
      nixosModules.default = import ./module.nix;
    };
}
