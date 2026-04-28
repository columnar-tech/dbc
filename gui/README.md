# dbc GUI

A Tauri v2 desktop application for managing ADBC drivers, built with Svelte 5 and Rust.

## Prerequisites

- **Rust** (stable, via [rustup](https://rustup.rs/))
- **Go** 1.25+ (for building the `dbc` sidecar)
- **Node.js** 20+ with npm
- **Tauri CLI** (installed via npm devDependency)

## Development Setup

```bash
cd gui
npm install
npm run tauri dev
```

## Building

```bash
# Build sidecar for host platform
bash gui/scripts/build-sidecar.sh

# Build Tauri bundle
cd gui && npm run tauri build
```

## Testing

```bash
cd gui/src-tauri && cargo test
cd gui && npm run check
cd gui/testdata/fixture-registry && go test ./...
go test ./...  # from repo root
```

## Release

Tag `gui-v*` triggers `.github/workflows/gui-release.yml` for all 5 target triples.

Required secrets: `MACOS_SIGN_P12`, `MACOS_SIGN_P12_PASSWORD`, `APPLE_SIGNING_IDENTITY`, `APPLE_ID`, `APPLE_PASSWORD`, `APPLE_TEAM_ID`.

## Troubleshooting

- **Logs**: Use the Logs page in the app
- **Credentials**: `~/.config/dbc/credentials.toml` (Linux/macOS)
- **Driver location**: `~/Library/Application Support/ADBC/Drivers` (macOS)
- **Registry override**: Set `DBC_BASE_URL` environment variable
- **Linux: app freezes when launched from VSCode integrated terminal**: VSCode's Electron environment pollutes library paths and wedges WebKitGTK during paint init. Launch `pixi run gui-dev` (or `npm run tauri dev`) from an external terminal (GNOME Terminal, Konsole, kitty, etc.) instead.
