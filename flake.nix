{
  description = "GWorX - Garry's Mod toolkit";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    nix-appimage.url = "github:ralismark/nix-appimage";
    nix-appimage.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    {
      self,
      nixpkgs,
      nix-appimage,
    }:
    let
      lib = nixpkgs.lib;
      version = "0.2.1";

      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = f: lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      appImageArch = {
        x86_64-linux = "x86_64";
        aarch64-linux = "aarch64";
      };

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
      vendorHash = "sha256-7F7kQ38wCMoo8bAT7+iKZwUbj+A9091cotrxWL8LSWs=";

      # Libraries Fyne (GLFW) needs for -tags wayland.
      # GLFW uses Wayland/EGL at runtime; libX11 remains a build dep because
      # go-gl's GL loader still includes GLX headers on Linux.
      guiLibs =
        pkgs: with pkgs; [
          libGL
          libX11
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
          tags = [ "wayland" ];
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

      # Valve's Steamworks redistributable for the Steamworks backend (unfree, redistributable).
      steamApiLib = {
        "x86_64-linux" = "linux64";
        "aarch64-linux" = "linuxarm64";
      };

      # gworx with libsteam_api in $out/lib/gworx, where the Steamworks backend looks
      # for it (like nwmpublisher's /usr/lib/nwmpublisher).
      # Repackages the gworx output instead of recompiling it. The binary is copied,
      # not symlinked: FindSteamAPI resolves symlinks before looking in ../lib/gworx.
      # wrapHostGL moves that file to libexec/gworx; ../lib/gworx still matches.
      mkGworxSteamworks =
        pkgs:
        let
          gworx = mkGworx pkgs;
        in
        pkgs.runCommand "gworx-steamworks-${version}"
          {
            pname = "gworx-steamworks";
            inherit version;
            meta = gworx.meta // {
              license = [
                lib.licenses.mit
                lib.licenses.unfreeRedistributable
              ];
            };
          }
          ''
            cp -r ${gworx} $out
            chmod -R u+w $out
            # ship Valve's file unmodified
            install -Dm644 ${./third_party/steamworks}/${
              steamApiLib.${pkgs.stdenv.hostPlatform.system}
            }/libsteam_api.so $out/lib/gworx/libsteam_api.so
          '';

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

      # On non-NixOS, Nix's libEGL only searches the Nix store for a GPU driver.
      # The launcher keeps that libEGL (it matches the binary's libc, including
      # when the host glibc is newer) and adds the host driver directories.
      # The real ELF moves to libexec so it stays one directory below $out:
      # FindSteamAPI looks at ../lib/gworx next to os.Executable.
      wrapHostGL =
        pkgs: pkg:
        let
          nixLibPath = lib.makeLibraryPath [
            pkgs.glibc
            pkgs.stdenv.cc.cc
            pkgs.libgcc
            pkgs.libglvnd
          ];
        in
        pkgs.runCommand "${pkg.pname}-${pkg.version}"
          {
            inherit (pkg) pname version meta;
          }
          ''
            mkdir -p $out
            cp -r ${pkg}/. $out/
            chmod -R u+w $out
            mkdir -p $out/libexec
            if [ -L "$out/bin/gworx" ]; then
              cp -L "$out/bin/gworx" "$out/libexec/gworx"
              rm "$out/bin/gworx"
            else
              mv "$out/bin/gworx" "$out/libexec/gworx"
            fi
            chmod 755 "$out/libexec/gworx"
            cat > "$out/bin/gworx" <<EOF
            #!/bin/sh
            export GWORX_REAL=$out/libexec/gworx
            export GWORX_NIX_LIB_PATH=${nixLibPath}
            . ${./packaging/host-gl.sh}
            EOF
            chmod 755 "$out/bin/gworx"
          '';
    in
    {
      packages = forAllSystems (
        pkgs:
        let
          system = pkgs.stdenv.hostPlatform.system;
          gworx = wrapHostGL pkgs (mkGworx pkgs);
          # Opt in to the unfree Steam API for this one package only.
          unfreePkgs = import nixpkgs {
            inherit system;
            config.allowUnfreePredicate = p: lib.getName p == "gworx-steamworks";
          };
          gworx-steamworks = wrapHostGL unfreePkgs (mkGworxSteamworks unfreePkgs);
        in
        {
          inherit gworx gworx-steamworks;
          gworx-cli = mkGworxCli pkgs;
          # Bundles gworx-steamworks + its closure (including libsteam_api) into one AppImage.
          # bin/gworx is the host-GL launcher; libexec/gworx is the real ELF.
          gworx-appimage = nix-appimage.lib.${system}.mkAppImage {
            program = lib.getExe gworx-steamworks;
            pname = "gworx";
            name = "gworx-${version}-${appImageArch.${system}}.AppImage";
          };
          default = gworx;
        }
      );

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.gworx}/bin/gworx";
        };
        steamworks = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.gworx-steamworks}/bin/gworx";
        };
        cli = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.gworx-cli}/bin/gworx-cli";
        };
      });

      overlays.default = final: prev: {
        gworx = wrapHostGL final (mkGworx final);
        gworx-cli = mkGworxCli final;
        gworx-steamworks = wrapHostGL final (mkGworxSteamworks final);
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
            # Match packaged Linux GUI builds (GLFW Wayland backend).
            export GOFLAGS="''${GOFLAGS:+$GOFLAGS }-tags=wayland"
          '';
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);
    };
}
