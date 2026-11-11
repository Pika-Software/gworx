{
  description = "GWorX - Garry's Mod toolkit";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      lib = nixpkgs.lib;
      version = "0.1.0";

      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = f: lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      src = lib.fileset.toSource {
        root = ./.;
        fileset = lib.fileset.unions [
          ./go.mod
          ./go.sum
          ./main.go
          ./gui.go
          ./gui_nogui.go
          ./internal
        ];
      };

      # Update with: nix build .#gworx 2>&1 | grep got:
      vendorHash = "sha256-DIgXyTpVfaJW1O5dWx8yna/qB2Fzsq2X490NZsGahMI=";

      # Libraries Fyne (GLFW) links against
      guiLibs =
        pkgs: with pkgs; [
          libGL
          libX11
          libXcursor
          libXrandr
          libXinerama
          libXi
          libXxf86vm
          libxkbcommon
          wayland
        ];

      meta = {
        description = "Garry's Mod toolkit: Workshop downloads/uploads and GMA packing";
        homepage = "https://github.com/Pika-Software/gworx";
        license = lib.licenses.mit;
        platforms = systems;
      };

      mkGworx =
        pkgs:
        pkgs.buildGoModule {
          pname = "gworx";
          inherit version src vendorHash;

          subPackages = [ "." ];
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
          ];

          nativeBuildInputs = with pkgs; [
            pkg-config
            copyDesktopItems
          ];
          buildInputs = guiLibs pkgs;

          desktopItems = [
            (pkgs.makeDesktopItem {
              name = "gworx";
              desktopName = "GWorX";
              comment = meta.description;
              exec = "gworx";
              icon = "applications-games";
              categories = [
                "Development"
                "Utility"
              ];
            })
          ];

          meta = meta // {
            mainProgram = "gworx";
          };
        };

      # CLI-only build: no Fyne, no cgo, no X11/GL dependencies.
      mkGworxCli =
        pkgs:
        pkgs.buildGoModule {
          pname = "gworx-cli";
          inherit version src vendorHash;

          subPackages = [ "." ];
          tags = [ "nogui" ];
          env.CGO_ENABLED = 0;
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
          ];

          postInstall = ''
            mv $out/bin/gworx $out/bin/gworx-cli
          '';

          meta = meta // {
            mainProgram = "gworx-cli";
          };
        };
    in
    {
      packages = forAllSystems (
        pkgs:
        let
          gworx = mkGworx pkgs;
        in
        {
          inherit gworx;
          gworx-cli = mkGworxCli pkgs;
          default = gworx;
        }
      );

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.gworx}/bin/gworx";
        };
        cli = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.gworx-cli}/bin/gworx-cli";
        };
      });

      overlays.default = final: prev: {
        gworx = mkGworx final;
        gworx-cli = mkGworxCli final;
      };

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages =
            with pkgs;
            [
              go
              gcc
              gopls
              delve
              pkg-config
              mesa
            ]
            ++ guiLibs pkgs;

          shellHook = ''
            export CGO_ENABLED=1
          '';
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);
    };
}
