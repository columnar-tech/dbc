# FAQ

## Where are drivers installed?

- **macOS**: `~/Library/Application Support/ADBC/Drivers`
- **Linux**: `~/.config/adbc/drivers`
- **Windows**: `%LocalAppData%\adbc\drivers`

## How do I use a custom registry?

Set the `DBC_BASE_URL` environment variable before launching the app:

```bash
DBC_BASE_URL=https://my-registry.example.com dbc-gui
```

## Can I install system-wide drivers?

The GUI only supports user-level installs. For system-wide installs, use the `dbc` CLI directly.

## Where are credentials stored?

Credentials are stored in `~/.config/dbc/credentials.toml` (Linux/macOS) or `%AppData%\dbc\credentials.toml` (Windows).

## How do I view command logs?

Open the **Logs** page to see recent command history with exit codes and error output.
