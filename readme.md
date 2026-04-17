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

## NixOS

Import the flake and the NixOS module:

```nix
# flake.nix inputs:
gguf-manager.url = "github:emanspeaks/gguf-manager";

# NixOS configuration:
imports = [ gguf-manager.nixosModules.default ];

services.gguf-manager = {
  enable         = true;
  package        = pkgs.gguf-manager; # or gguf-manager.packages.${system}.default
  port           = 9293;
  modelsDir      = "/var/lib/llama-models";
  llamaServerURL = "http://localhost:9292";
  llamaService   = "llama-cpp.service";
  hfToken        = "";               # set if needed
};
```

The service runs as the `llama-cpp` user in the `llm` group and is granted a
polkit rule that lets it restart `llama-cpp.service` via D-Bus without root.

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

## Building

```sh
go build -o gguf-manager .
```

Requires Go 1.22+.
