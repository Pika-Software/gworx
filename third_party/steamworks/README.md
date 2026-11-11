# Steamworks SDK redistributables

`libsteam_api` binaries from Valve's Steamworks SDK. GWorX loads them at runtime
(purego, no cgo/linking) for the Steamworks backend and ships the one for the
target platform next to the `gworx` binary, the same way
[nwmpublisher](https://github.com/DeisDev/nwmpublisher) does.

| Folder | Platform |
|---|---|
| `linux64/libsteam_api.so` | Linux x86-64 |
| `linuxarm64/libsteam_api.so` | Linux arm64 |
| `win64/steam_api64.dll` | Windows x64 (no Windows arm64 build exists) |
| `osx/libsteam_api.dylib` | macOS, universal (x86-64 + arm64) |

Source: the [`steamworks-sys` 0.13.0](https://crates.io/crates/steamworks-sys/0.13.0)
crate (`lib/steam/redistributable_bin`). The crate checksum
(`ae139f05…88131e9`) matches the crates.io index. The linux64, win64 and osx
files are byte-identical to the ones nwmpublisher commits. Flat API interfaces:
`SteamUGC_v021`, `SteamUser_v023`, `SteamFriends_v018`, the same as the library
Garry's Mod ships.

Copyright © Valve Corporation. Redistributed with this application under the
Steamworks SDK Access Agreement. See `SHA256SUMS` to verify the files.
