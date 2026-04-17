# gguf-manager

A self-contained Go web application for managing GGUF model files used by a
[llama-server](https://github.com/ggml-org/llama.cpp) instance running in router
mode (`--models-dir`).

## Features

- **Browse and download** GGUF models from HuggingFace — paste a repo ID, pick a
  quantization, and stream the download in real time
- **View local models** with total size and loaded/unloaded status (cross-referenced
  against `/v1/models` on your llama-server)
- **Delete models** and automatically restart `llama-cpp.service` via D-Bus
- **Manually restart** the llama-server from the UI at any time
- Single binary with an embedded frontend — no separate build step, no Node.js

## Environment

| Assumption | Value |
|---|---|
| llama-server URL | `http://localhost:9292` |
| Models directory | `/var/lib/llama-models/` |
| `hf` binary | `python3Packages.huggingface-hub` on PATH |
| Init system | systemd |

Each model lives in its own subdirectory named after the model, e.g.
`/var/lib/llama-models/Qwen3-Coder-Q8_0/`.

## Running

```sh
# With defaults (no config file needed):
gguf-manager

# With a config file:
gguf-manager --config /etc/gguf-manager.json
```

Open `http://localhost:9293` in your browser.

## Configuration

All fields are optional. Create a JSONC file (comments allowed) at any path and
pass it with `--config`:

```jsonc
{
  // Path to the directory that holds per-model subdirectories
  "modelsDir": "/var/lib/llama-models",

  // llama-server base URL
  "llamaServerURL": "http://localhost:9292",

  // systemd service to restart after downloads / deletes
  "llamaService": "llama-cpp.service",

  // Port this app listens on
  "port": 9293,

  // Optional HuggingFace token for private repos or higher rate limits
  "hfToken": ""
}
```

## Polkit setup

gguf-manager restarts llama-server via D-Bus (`org.freedesktop.systemd1.manage-units`).
Without a polkit rule granting this permission to the process user, you'll see
`connection reset by peer` in the status bar when restarting or after a download.

### NixOS — using the service module

Enable `services.gguf-manager` (see [NixOS](#nixos) section below). The module
installs the polkit rule automatically for the service user. No further action needed.

### NixOS — running the binary directly

If you're running the binary outside of the NixOS module (e.g. from a nix shell,
`nix run`, or a hand-written systemd unit), the polkit rule is **not** installed
automatically. Add it to your `configuration.nix`:

```nix
security.polkit.extraConfig = ''
  polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units" &&
        action.lookup("unit") == "llama-cpp.service" &&
        subject.user == "gguf-manager") {
      return polkit.Result.YES;
    }
  });
'';
```

Replace `gguf-manager` with whatever user runs the binary, and
`llama-cpp.service` with your actual service name if it differs. Then rebuild:

```sh
sudo nixos-rebuild switch
```

### Other systemd distros

Drop a rules file into `/etc/polkit-1/rules.d/`:

```sh
sudo tee /etc/polkit-1/rules.d/50-gguf-manager.rules <<'EOF'
polkit.addRule(function(action, subject) {
  if (action.id == "org.freedesktop.systemd1.manage-units" &&
      action.lookup("unit") == "llama-cpp.service" &&
      subject.user == "your-username") {
    return polkit.Result.YES;
  }
});
EOF
```

Replace `your-username` and `llama-cpp.service` as above. Polkit picks up new
rules files without a restart.

## NixOS

Import the flake and the NixOS module:

```nix
# flake.nix inputs
gguf-manager.url = "github:emanspeaks/gguf-manager";
```

```nix
# NixOS configuration
imports = [ gguf-manager.nixosModules.default ];

services.gguf-manager = {
  enable         = true;
  package        = gguf-manager.packages.${system}.default;
  modelsDir      = "/var/lib/llama-models";
  llamaServerURL = "http://localhost:9292";
  llamaService   = "llama-cpp.service";
  # hfToken      = "";  # set if needed
};
```

The module does the following automatically when enabled:

- Creates a `gguf-manager` system user in the `llm` group
- Sets `HF_HOME=/var/lib/gguf-manager` so the `hf` tool can write its cache
  (without this it tries to write to `/.cache` and fails)
- Creates `/var/lib/gguf-manager` as a persistent state directory owned by the
  service user
- Creates `modelsDir` (`/var/lib/llama-models` by default) as a group-writable
  directory so the service user can write model subdirectories into it
- Installs a polkit rule allowing the service user to restart `llamaService` via
  D-Bus without root

### Required: add the service user to your llm group

The `llm` group must exist and `modelsDir` must be writable by it. Make sure your
`configuration.nix` includes the group and any other users who need access:

```nix
users.groups.llm.members = [ "your-username" "llama-cpp" "gguf-manager" ];
```

If `modelsDir` is managed by your llama-cpp setup rather than the gguf-manager
module, ensure it has `0775` permissions with group `llm`:

```nix
systemd.tmpfiles.rules = [
  "d /var/lib/llama-models 0775 llama-cpp llm -"
];
```

If you run the binary outside the module, see the
[Polkit setup](#polkit-setup) section above.

### Nix dependencies

The flake uses [gomod2nix](https://github.com/nix-community/gomod2nix) instead
of a manual `vendorHash`. The `gomod2nix.toml` lockfile is regenerated
automatically by CI whenever `go.mod` or `go.sum` change. No manual hash
management required.

To regenerate it locally after updating dependencies:

```sh
gomod2nix generate
```

`gomod2nix` is included in the flake's `devShell`.

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/local` | List local models with size and loaded status |
| `GET` | `/api/repo?id={owner/repo}` | List GGUF files in a HuggingFace repo |
| `POST` | `/api/download` | Start a download `{"repoId":"…","filename":"…"}` |
| `GET` | `/api/download/status` | SSE stream of download output |
| `DELETE` | `/api/local/{name}` | Delete a model directory |
| `GET` | `/api/status` | App state: llama reachability, download in progress |
| `POST` | `/api/restart` | Restart the configured llama service via D-Bus |

## Building

```sh
go build -o gguf-manager .
```

Requires Go 1.22+.
