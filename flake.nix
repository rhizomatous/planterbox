{
  # dev environment & package for planterbox
  description = "planterbox: a Nix-based sandbox for running coding agents";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      # keep in sync with the latest release tag.
      version = "0.10.0";
      forAllSystems = f:
        nixpkgs.lib.genAttrs [
          "x86_64-linux"
          "aarch64-linux"
          "x86_64-darwin"
          "aarch64-darwin"
        ] (system: f nixpkgs.legacyPackages.${system});
      # build the plbx binary against a given package set. shared by the
      # per-system packages and the overlay.
      mkPlbx = pkgs: pkgs.buildGoModule {
        pname = "plbx";
        inherit version;
        src = ./.;
        vendorHash = "sha256-4eXMWglbQHP4kIjkIK0CYLU862/zRPFYUobmUYkdFfQ=";
        subPackages = [ "cmd/plbx" "cmd/plbxd" ];
        # inject the version into the same symbol the Makefile uses.
        ldflags = [ "-s" "-w" "-X" "main.version=${version}" ];
        meta = {
          description = "a Nix-based sandbox for running coding agents in isolated containers";
          homepage = "https://github.com/rhizomatous/planterbox";
          license = pkgs.lib.licenses.mit;
          mainProgram = "plbx";
        };
      };
    in {
      # `overlays.default` lets consumers get `pkgs.plbx` after applying it.
      overlays.default = final: _prev: {
        plbx = mkPlbx final;
      };

      packages = forAllSystems (pkgs: {
        default = self.packages.${pkgs.system}.plbx;
        plbx = mkPlbx pkgs;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            git
            gnumake
            golangci-lint # lint & static analysis
            gofumpt # stricter gofmt
            lefthook # git hooks manager
            goreleaser # release build & publish
            protobuf # protoc, for the daemon's wire contract
            protoc-gen-go # go message codegen
            protoc-gen-go-grpc # go service codegen
          ];
          shellHook = ''
            # install the git hooks defined in lefthook.yml
            lefthook install >/dev/null 2>&1 || true
            echo "🪴 planterbox dev shell (go $(go version | cut -d' ' -f3))"
          '';
        };
      });
    };
}
