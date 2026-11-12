# GWorX

GWorX is a toolkit for Garry's Mod. It downloads addons from the Steam Workshop,
uploads your own addons, and builds or extracts `.gma` files. You can use it
from a desktop app (built with [Fyne](https://fyne.io)) or from the command
line. GMA files are read and written with
[go-gma](https://github.com/unknown-gd/go-gma).

## Features

- **Steam Workshop**
  - **Download**: paste a Workshop link or ID. The addon is saved as a plain
    `.gma` (old LZMA-compressed addons are decompressed) and, by default,
    extracted into the `uploads` folder. Untick "Extract files to the uploads
    folder" to keep only the raw GMA.
  - **Upload**: upload a finished GMA, or build one from a folder and upload it.
    Leave the item ID empty to create a new item.
  - **My Addons**: addons you published. You can update, open, delete or forget
    them. "Refresh from Steam" imports items you published before.
  - **History**: every addon you downloaded with GWorX.
- **GMA Tools**
  - **Extract**: drop a `.gma` (or a legacy `.bin`) onto the window. You see a
    numbered list of its files and the addon's details (title, author,
    version, required content, sizes). **Verify checksums** marks each file
    as OK or damaged, and **Extract** unpacks the addon into a folder.
  - **Build**: set the title, description, author, addon version, type, tags,
    required content (Workshop IDs), addon folder and ignore wildcards
    (`*.txt`, `.git`, `app.exe`, ...). Reads and writes `addon.json`. You can
    also drop an addon folder onto the window.
- **Dedicated Server**: install and run Garry's Mod dedicated servers with
  steamcmd. Each server has its own name and folder, a live console with a
  command line, Start/Stop/Restart, a branch selector, and its gamemode,
  level, max players, port, tickrate and `developer` value.
- **Command line**: the Workshop and GMA features also work without the GUI.

## Installation

| Platform    | GUI + CLI                                       | CLI only                                      |
| ----------- | ----------------------------------------------- | --------------------------------------------- |
| Linux       | tarball, AppImage, Flatpak                      | `gworx-cli-<ver>-linux-{amd64,arm64}.tar.gz`  |
| Windows     | `gworx-<ver>-windows-amd64.zip`                 | `gworx-cli-<ver>-windows-{amd64,arm64}.zip`   |
| macOS       | `gworx-<ver>-darwin-{arm64,amd64}.tar.gz`       | `gworx-cli-<ver>-darwin-{arm64,amd64}.tar.gz` |
| NixOS / Nix | `packages.default`, `packages.gworx-steamworks` | `packages.gworx-cli`                          |

All archives are on the [Releases](https://github.com/Pika-Software/gworx/releases)
page. Each release has a `SHA256SUMS` file you can check the downloads against:

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

The `gworx-cli` builds have no GUI and depend on no system libraries, so they
work on headless servers and in CI.

Every archive except Windows arm64 also contains Valve's Steam API library
(`libsteam_api.so`, `steam_api64.dll` or `libsteam_api.dylib`) for the
[Steamworks backend](#workshop-backends). Keep it in the same folder as the
binary.

You also need a Workshop backend. See [Workshop backends](#workshop-backends).

### Linux

**Flatpak** (app id `eu.p1ka.gworx`, includes the Steam API library):

```sh
flatpak install --user gworx-*-x86_64.flatpak
flatpak run eu.p1ka.gworx
```

**AppImage** (includes the Steam API library): download
`gworx-<ver>-x86_64.AppImage`, mark it executable, and run it:

```sh
chmod +x gworx-*-x86_64.AppImage
./gworx-*-x86_64.AppImage
```

The AppImage needs Linux user namespaces (enabled on most desktops). On non-NixOS
systems the GUI may also need a working OpenGL driver from the host.

**tarball**: the GUI build needs OpenGL and the X11 libraries. Most desktops
already have them. If not, install them:

```sh
# Debian / Ubuntu
sudo apt install libgl1 libx11-6 libxcursor1 libxrandr2 libxinerama1 libxi6 libxxf86vm1
# Fedora
sudo dnf install mesa-libGL libX11 libXcursor libXrandr libXinerama libXi libXxf86vm
# Arch
sudo pacman -S mesa libx11 libxcursor libxrandr libxinerama libxi libxxf86vm
```

Then unpack the archive and put the binary on your `PATH`:

```sh
tar -xzf gworx-*-linux-amd64.tar.gz
install -Dm755 gworx-*-linux-amd64/gworx ~/.local/bin/gworx
```

To install the CLI-only build instead, use the same steps with the `gworx-cli-*`
archive (the binary inside is `gworx-cli`).

### Windows

1. Unzip `gworx-<ver>-windows-amd64.zip` and run `gworx.exe`.
2. For the command line, use `gworx-cli.exe` from the `gworx-cli-*` archive.
   `gworx.exe` is built without a console window, so it prints nothing when
   started from a terminal.

The binaries are not signed. If SmartScreen blocks the app, click
**More info → Run anyway**.

### macOS

```sh
tar -xzf gworx-*-darwin-arm64.tar.gz      # use darwin-amd64 on Intel Macs
xattr -d com.apple.quarantine gworx-*-darwin-arm64/gworx
sudo install -m755 gworx-*-darwin-arm64/gworx /usr/local/bin/gworx
```

The binary is not signed, so you have to remove the quarantine flag (the
`xattr` line above) before macOS will run it.

### Nix

Run it without installing:

```sh
nix run github:Pika-Software/gworx              # GUI
nix run github:Pika-Software/gworx#cli -- help  # CLI only
```

Install into your profile:

```sh
nix profile install github:Pika-Software/gworx                   # GUI + CLI
nix profile install github:Pika-Software/gworx#gworx-steamworks  # GUI + CLI + Steam API
nix profile install github:Pika-Software/gworx#gworx-cli         # CLI only
```

| Package            | Contents                                                                                                             |
| ------------------ | -------------------------------------------------------------------------------------------------------------------- |
| `gworx` (default)  | GUI + CLI. The Steamworks backend uses Garry's Mod's library or a path you set.                                      |
| `gworx-steamworks` | The same plus Valve's Steam API in `lib/gworx`. Unfree (redistributable). The flake allows it for this package only. |
| `gworx-appimage`   | AppImage of `gworx-steamworks` (closure + Steam API). Build with `nix build .#gworx-appimage`.                       |
| `gworx-cli`        | CLI only.                                                                                                            |

#### flake.nix (NixOS)

```nix
inputs.gworx.url = "github:Pika-Software/gworx";

environment.systemPackages = [
  gworx.packages.${pkgs.stdenv.hostPlatform.system}.default    # GUI + CLI
  # gworx.packages.${pkgs.stdenv.hostPlatform.system}.gworx-cli  # CLI only
];
```

#### Home Manager

```nix
home.packages = [ inputs.gworx.packages.${pkgs.stdenv.hostPlatform.system}.default ];
```

#### Overlay

```nix
nixpkgs.overlays = [ inputs.gworx.overlays.default ];
environment.systemPackages = [ pkgs.gworx ];  # or pkgs.gworx-cli / pkgs.gworx-steamworks
```

Through the overlay, `gworx-steamworks` follows your own nixpkgs config, so it
needs `allowUnfree` (or an `allowUnfreePredicate` that allows it).

The flake supports `x86_64-linux` and `aarch64-linux`. steamcmd is unfree, so
the flake doesn't include it. To use steamcmd, add it yourself:

```nix
nixpkgs.config.allowUnfree = true;   # or allowUnfreePredicate for steamcmd only
environment.systemPackages = [ pkgs.steamcmd ];
```

### With Go

The CLI only needs Go 1.26 or newer:

```sh
CGO_ENABLED=0 go install -tags nogui github.com/Pika-Software/gworx@latest
```

The GUI also needs a C compiler plus the OpenGL/X11 development headers
(`libgl1-mesa-dev xorg-dev` on Debian/Ubuntu):

```sh
go install github.com/Pika-Software/gworx@latest
```

### From source

```sh
git clone https://github.com/Pika-Software/gworx
cd gworx
go build .                                     # GUI + CLI -> ./gworx
CGO_ENABLED=0 go build -tags nogui -o gworx-cli .   # CLI only
# or: nix build .#gworx / nix build .#gworx-cli
```

## Running

### GUI

Start `gworx` with no arguments. You can also use **GWorX** from the
application menu if you installed with Nix, or run `gworx gui`.

Set it up once:

1. Open **Settings** and choose a **Workshop backend**.
2. **steamcmd**: enter your Steam username and click **Log in to Steam…**.
   Type your password and Steam Guard code, or confirm the login in the Steam
   mobile app. You only need to log in to upload. Downloads work anonymously
   if you tick "Download anonymously".
3. **Steamworks**: start the Steam client and click **Test connection**.
   Release builds use the Steam API library next to the binary. Leave the path
   empty unless you want to use a different one.
4. Optional: change the data folders, the default author, and the default
   ignore list.
5. Click **Save**.

Common tasks:

- **Download an addon**: go to **Steam Workshop → Download**, paste a link or
  ID, then click **Download**.
- **Publish an addon**: go to **Steam Workshop → Upload** and pick **Build the
  GMA from a folder**. Fill in the form, choose a preview image, then click
  **Upload**.
- **Update an addon**: go to **My Addons**, select it and click **Update…**. The
  form fills in from the last upload and the addon version goes up by one.
- **Unpack a GMA**: drop it anywhere on the window. It opens in **GMA Tools →
  Extract**.
- **Pack a GMA**: drop the addon folder onto the window, or use **GMA Tools →
  Build**.

File and folder pickers use your system's dialog: the desktop portal on
Linux (GNOME, KDE, ...), or `zenity`/`kdialog` when there's no portal, and the
native dialogs on Windows and macOS. If none is available, GWorX uses its
built-in dialog.

The **Log** tab shows full output, including everything steamcmd prints.

### Command line

```sh
gworx help                                # list all commands
gworx <command> -h                        # flags for one command

# Workshop
gworx download 104603291                  # download, then extract to the uploads folder
gworx download -no-extract <link> <link>  # several items, raw GMAs only
gworx download -o ./out <id>              # extract into a folder of your choice
gworx login -user <steam user>            # log in once (steamcmd caches the login)
gworx upload -preview icon.jpg -visibility unlisted ./my_addon   # build + publish a new item
gworx upload -id 123456789 -note "fixes" ./my_addon             # update an item
gworx upload -id 123456789                # rebuild from the folder used last time
gworx upload -id 123456789 addon.gma      # upload a finished GMA
gworx published -refresh                  # list your items (Steamworks or Web API key)
gworx delete 123456789                    # Steamworks only
gworx history

# GMA
gworx build -type tool -tags fun,build ./my_addon   # reads addon.json, flags override it
gworx build -n ./my_addon                 # show which files would be packed
gworx build -save-json ./my_addon         # also write addon.json
gworx extract -o ./out addon.gma
gworx info -files addon.gma

# Settings
gworx config                              # print the config
gworx config set backend steamworks
gworx config set steam_username <user>
gworx config set auto_extract false
```

Flags you can use on most commands:

- `-backend steamcmd|steamworks`: use this backend for one run, ignoring the config.
- `-q`: print only results and errors.
- `-json`: print machine-readable output.

Exit codes:

- `0`: success.
- `1`: error.
- `2`: wrong usage.
- `130`: interrupted.

Progress and logs go to stderr. Results (file paths, item IDs) go to stdout, so
you can use them in scripts:

```sh
id=$(gworx upload -q -id 123456789 ./my_addon)
gma=$(gworx download -q -no-extract 104603291)
```

### Dedicated servers

Open the **Dedicated Server** tab and click **New server…**:

1. Give the server a name and a location folder (default:
   `~/gworx/servers/<name>`). To manage a server you already have, pick its
   folder instead.
2. Choose a branch. `x86-64` is the 64-bit server; `public`, `prerelease` and
   `dev` are also available, or type another branch name.
3. Confirm the install. steamcmd downloads app 4020 (a few GB) into the
   folder, and its output appears in the console.

Each server's page has:

- **Start / Stop / Restart**: Stop sends `quit` and waits up to 20 seconds,
  then terminates the server.
- **Install / Update**: runs steamcmd again. Tick **Validate files** to check
  every file. Run it after changing the branch.
- **Open folder**: opens the server's location.
- **Settings**: branch, gamemode, level, max players, port, tickrate,
  `developer` (0/1/2), Workshop collection (`+host_workshop_collection`),
  P2P (`-p2p`), and extra arguments. **Startup command** shows the exact
  command line. Changes are saved when you start or restart the server.
- **Console**: live server output. Type a command and press Enter (or
  **Send**).

Global settings live in **Settings → Dedicated servers**:

- **Steam Account Token**: a
  [game server login token](https://steamcommunity.com/dev/managegameservers)
  for app 4000. When set, every server starts with
  `+sv_setsteamaccount <token>`. The startup command preview hides it. Without
  a token the server runs anonymously and ranks lower in the server browser.
- **Launch wrapper**: a command put in front of `srcds_run`. It defaults to
  `steam-run` on NixOS, because Valve's server binaries can't find their
  libraries there otherwise.

Two servers can't run on the same port at once. When you quit GWorX while
servers are running, it asks first and then stops them. On Windows,
`srcds.exe` writes most of its output to its own console window, so the
GWorX console shows less there.

### Files

| What                                               | Where                                                                                           |
| -------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| Config and library (`config.json`, `library.json`) | Linux `~/.config/gworx`, macOS `~/Library/Application Support/gworx`, Windows `%AppData%\gworx` |
| Downloaded GMAs                                    | `~/gworx/downloads`                                                                             |
| Extracted addons                                   | `~/gworx/uploads`                                                                               |
| Built GMAs                                         | `~/gworx/builds`                                                                                |
| Dedicated servers (default location)               | `~/gworx/servers/<name>` (list in `servers.json` next to the config)                            |
| steamcmd workshop cache                            | `~/gworx/steamcmd`                                                                              |

You can change the folders in **Settings** or with `gworx config set data_dir …`.

## Development

```sh
nix develop                              # Go, gcc and the GUI libraries
go run .                                 # GUI
go run . download 104603291              # CLI
go test ./...
CGO_ENABLED=0 go build -tags nogui .     # CLI-only build
```

Code layout:

- `internal/core`: the workflows shared by the GUI and the CLI.
- `internal/ui`: the Fyne GUI.
- `internal/cli`: the commands.
- `internal/workshop`: the steamcmd and Steamworks backends and the Web API client.
- `internal/gmaops`: building and extracting GMAs.

## License

[MIT](LICENSE). The Steam API binaries in `third_party/steamworks` belong to
Valve Corporation and are redistributed under the Steamworks SDK Access
Agreement.
