# w84ggufman

A self-contained Go web application for managing GGUF model files used by a
[llama-server](https://github.com/ggml-org/llama.cpp) instance running in router mode (`--models-dir`) or a
[llama-swap](https://github.com/mostlygeek/llama-swap) proxy.

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
| --- | --- |
| llama-server URL | `http://localhost:9292` |
| Models directory | `/var/lib/llama-models/` |
| `hf` binary | `python3Packages.huggingface-hub` on PATH |
| Init system | systemd |

Each model lives in its own subdirectory named after the model, e.g.
`/var/lib/llama-models/Qwen3-Coder-Q8_0/`.

## Running

```sh
# With defaults (no config file needed):
w84ggufman

# With a config file:
w84ggufman --config /etc/w84ggufman.json
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

  // systemd service name for w84ggufman itself — enables the
  // "Restart w84ggufman" button in the UI. Set to "" to disable.
  "selfService": "w84ggufman.service",

  // Port this app listens on
  "port": 9293,

  // Optional HuggingFace token for private repos or higher rate limits
  "hfToken": "",

  // Warn before downloading files larger than this (0 = disabled)
  "warnDownloadGiB": 10.0,

  // Total GPU/unified memory available to the model in GiB.
  // 0 = auto-detect via nvidia-smi / AMD sysfs / Apple sysctl.
  // On systems with dynamically allocated unified memory (AMD APU with TTM
  // pages_limit, etc.) auto-detection reads the hardware total rather than
  // the active allocation, so set this manually to your actual limit.
  "vramGiB": 0,

  // Quant tiles whose total size exceeds this % of vramGiB are highlighted
  // with an amber warning, and the download confirmation dialog is shown.
  // Default: 80. Set to 0 to disable.
  "warnVramPercent": 80,

  // Path to the llama-swap config.yaml to keep in sync with downloads and
  // deletes. When set, w84ggufman writes model entries to this file (in
  // addition to models.ini) whenever a model is downloaded or deleted.
  // llama-swap reloads the file automatically — no restart needed.
  // Leave empty (the default) to disable llama-swap config management.
  "llamaSwapConfig": "/ai/llama-swap/config.yaml"
}
```

## VRAM / memory warnings

When `vramGiB` is known (either auto-detected or set in config), quant tiles in
the model browser are color-coded:

| Tile style | Meaning |
| --- | --- |
| Normal | Fits comfortably within VRAM |
| Amber border + ⚠ | Exceeds `warnVramPercent`% of your VRAM (default 80%) |
| Grayed out | Would exceed free disk space |

A confirmation dialog is shown before starting a download that exceeds the VRAM
threshold.

### AMD APU / unified memory and TTM

On AMD APUs (and similar unified-memory GPUs) the kernel TTM subsystem manages a
shared memory pool whose size is set at boot via kernel parameters rather than
fixed hardware. w84ggufman auto-detects this by reading
`/sys/module/ttm/parameters/pages_limit` (value in 4 KiB pages) **before**
falling back to `mem_info_vram_total`, which reports the full hardware DRAM
capacity rather than the active TTM allocation.

If you have set the TTM limit via kernel parameters, auto-detection should work:

```sh
# NixOS: set TTM allocation at boot
boot.kernelParams = [
  "ttm.pages_limit=30000000"   # pages × 4 KiB = 114.4 GiB
  "ttm.page_pool_size=30000000"
];
```

```sh
# Verify what the kernel sees:
cat /sys/module/ttm/parameters/pages_limit
# → 30000000

# Convert to GiB:
python3 -c "print(30_000_000 * 4096 / 1024**3, 'GiB')"
# → 114.44 GiB
```

If auto-detection still reports the wrong value (e.g. on a system where TTM
parameters are set in a non-standard way), override with `vramGiB` in config or
the NixOS module option.

## Polkit setup

w84ggufman restarts llama-server (and optionally itself) via D-Bus
(`org.freedesktop.systemd1.manage-units`). Without a polkit rule granting this
permission to the process user, you'll see `connection reset by peer` in the
status bar when restarting or after a download. The "Restart w84ggufman" button
also requires the polkit rule to cover `selfService`.

### NixOS — using the service module

Enable `services.w84ggufman` (see [NixOS](#nixos) section below). The module
installs the polkit rule automatically, covering both `llamaService` and
`selfService`. No further action needed.

### NixOS — running the binary directly

If you're running the binary outside the NixOS module (e.g. from a nix shell,
`nix run`, or a handwritten systemd unit), the polkit rule is **not** installed
automatically. Add it to your `configuration.nix`:

```nix
security.polkit.extraConfig = ''
  polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units" &&
        ["llama-cpp.service", "w84ggufman.service"].indexOf(action.lookup("unit")) >= 0 &&
        subject.user == "w84ggufman") {
      return polkit.Result.YES;
    }
  });
'';
```

Replace `w84ggufman` with whatever user runs the binary, and adjust the service
names to match your setup. Omit `w84ggufman.service` from the list (or set
`"selfService": ""` in config) if you don't want the UI self-restart feature.
Then rebuild:

```sh
sudo nixos-rebuild switch
```

### Other systemd distros

Drop a rules file into `/etc/polkit-1/rules.d/`:

```sh
sudo tee /etc/polkit-1/rules.d/50-w84ggufman.rules <<'EOF'
polkit.addRule(function(action, subject) {
  if (action.id == "org.freedesktop.systemd1.manage-units" &&
      ["llama-cpp.service", "w84ggufman.service"].indexOf(action.lookup("unit")) >= 0 &&
      subject.user == "your-username") {
    return polkit.Result.YES;
  }
});
EOF
```

Replace `your-username` and the service names as above. Polkit picks up new
rules files without a restart.

## NixOS

### flake.nix

Add w84ggufman as a flake input and import the NixOS module:

```nix
{
  inputs = {
    nixpkgs.url     = "github:NixOS/nixpkgs/nixos-unstable";
    w84ggufman.url = "github:emanspeaks/w84ggufman";
    # ... your other inputs
  };

  outputs = inputs@{ self, nixpkgs, w84ggufman, ... }: {
    nixosConfigurations.myhostname = nixpkgs.lib.nixosSystem {
      modules = [
        ./configuration.nix
        w84ggufman.nixosModules.default
        {
          services.w84ggufman = {
            enable         = true;
            package        = w84ggufman.packages.x86_64-linux.default;
            modelsDir      = "/var/lib/llama-models";
            llamaServerURL = "http://localhost:9292";
            llamaService   = "llama-cpp.service";
            # selfService = "w84ggufman.service";  # default; enables UI self-restart
            # hfToken = "hf_...";  # see note below about secrets

            # VRAM warnings — set vramGiB manually when using unified memory
            # with a dynamic TTM allocation (AMD APU, etc.); auto-detection
            # reads the hardware total, not your pages_limit allocation.
            # vramGiB        = 115.0;  # your TTM allocation in GiB
            # warnVramPercent = 80;    # default; warn above 80% of vramGiB
          };
        }
      ];
    };
  };
}
```

> **HF token**: avoid committing real tokens in flake.nix. Use a secrets manager
> such as [agenix](https://github.com/ryantm/agenix) or
> [sops-nix](https://github.com/Mic92/sops-nix), or set `HF_TOKEN` in your
> service environment from a secrets file.

### configuration.nix additions

The module creates the `w84ggufman` system user automatically, but you need
to ensure:

**1. The `llm` group exists and includes the right members.**
If you use `services.llama-cpp`, that module creates the `llm` group. Add your
own username and any other users who need model access:

```nix
users.groups.llm.members = [ "your-username" "llama-cpp" "w84ggufman" ];
```

**2. The models directory exists and is group-writable by `llm`.**
This is typically managed by your llama-cpp setup. If not already present, add
a tmpfiles rule — note the directory should be owned by `llama-cpp` (or
whatever user llama-server runs as), not `w84ggufman`:

```nix
systemd.tmpfiles.rules = [
  "d /var/lib/llama-models 0775 llama-cpp llm -"
];
```

The module creates the `.hf-cache` subdirectory inside `modelsDir` automatically,
so you do **not** need a tmpfiles rule for it.

### What the module handles automatically

| Thing | How |
| --- | --- |
| `w84ggufman` system user | `users.users.w84ggufman` with `isSystemUser = true` |
| `HF_HOME` | Set to `${modelsDir}/.hf-cache` so `hf` never tries to write to `/.cache` |
| `.hf-cache` directory | Created via `systemd.tmpfiles` owned by the service user |
| polkit rule | Allows service user to restart `llamaService` and `selfService` via D-Bus without root |

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
| --- | --- | --- |
| `GET` | `/api/local` | List local models with size and loaded status |
| `GET` | `/api/repo?id={owner/repo}` | List GGUF files in a HuggingFace repo |
| `POST` | `/api/download` | Start a download `{"repoId":"…","filename":"…"}` |
| `GET` | `/api/download/status` | SSE stream of download output |
| `DELETE` | `/api/local/{name}` | Delete a model directory |
| `GET` | `/api/status` | App state: llama reachability, download in progress |
| `POST` | `/api/restart` | Restart the configured llama service via D-Bus |
| `POST` | `/api/restart-self` | Restart the w84ggufman service itself via D-Bus |
| `GET` | `/api/preset/raw/{name}` | Get the raw INI block for a model |
| `PUT` | `/api/preset/raw/{name}` | Replace the raw INI block for a model |

## Building

```sh
go build -o w84ggufman .
```

Requires Go 1.22+.
