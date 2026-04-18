{
  description = "w84ggufman — local web UI for managing GGUF models with llama-server";

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
        version = pkgs.lib.trim (builtins.readFile ./VERSION);
      in {
        packages.default = buildGoApplication {
          pname   = "w84ggufman";
          version = version;
          src     = ./.;
          modules = ./gomod2nix.toml;
          ldflags = [ "-s" "-w" "-X" "main.version=v${version}" ];

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
