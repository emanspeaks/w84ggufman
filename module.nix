{ config, lib, pkgs, ... }:

let
  cfg = config.services.w84ggufman;
  # Build list of units the service user is allowed to restart.
  # Filter out any empty strings so that setting selfService = "" disables self-restart.
  allowedUnits   = lib.filter (u: u != "") [ cfg.llamaService cfg.selfService ];
  allowedUnitsJS = "[" + lib.concatMapStringsSep "," (u: "\"${u}\"") allowedUnits + "]";
  configFile = pkgs.writeText "w84ggufman.json" (builtins.toJSON {
    modelsDir         = cfg.modelsDir;
    llamaServerURL    = cfg.llamaServerURL;
    llamaService      = cfg.llamaService;
    selfService       = cfg.selfService;
    port              = cfg.port;
    hfToken           = cfg.hfToken;
    warnDownloadGiB   = cfg.warnDownloadGiB;
    vramGiB           = cfg.vramGiB;
    warnVramPercent   = cfg.warnVramPercent;
    llamaSwapConfig   = cfg.llamaSwapConfig;
    atopwebURL              = cfg.atopwebURL;
    llamaServerLandingPage  = cfg.llamaServerLandingPage;
  });
in {
  options.services.w84ggufman = {
    enable = lib.mkEnableOption "w84ggufman local model manager UI";

    package = lib.mkOption {
      type        = lib.types.package;
      description = "The w84ggufman package to use.";
    };

    port = lib.mkOption {
      type    = lib.types.port;
      default = 9293;
      description = "Port the web UI listens on.";
    };

    modelsDir = lib.mkOption {
      type    = lib.types.str;
      default = "/var/lib/llama-models";
      description = "Path to the directory containing model subdirectories. Must be group-writable by serviceGroup.";
    };

    hfHome = lib.mkOption {
      type    = lib.types.str;
      default = "${cfg.modelsDir}/.hf-cache";
      description = "Path for HuggingFace Hub state (HF_HOME). Created automatically; parent directory must already exist.";
    };

    llamaServerURL = lib.mkOption {
      type    = lib.types.str;
      default = "http://localhost:9292";
      description = "Base URL of the llama-server instance.";
    };

    llamaService = lib.mkOption {
      type    = lib.types.str;
      default = "llama-cpp.service";
      description = "systemd service name to restart after model changes.";
    };

    selfService = lib.mkOption {
      type    = lib.types.str;
      default = "w84ggufman.service";
      description = ''
        systemd service name for the w84ggufman process itself.
        Used by the "Restart w84ggufman" UI button. Set to "" to disable self-restart.
      '';
    };

    hfToken = lib.mkOption {
      type    = lib.types.str;
      default = "";
      description = "Optional HuggingFace token for private repos or higher rate limits.";
    };

    warnDownloadGiB = lib.mkOption {
      type    = lib.types.float;
      default = 10.0;
      description = "Prompt for confirmation before downloading files larger than this many GiB. Set to 0 to disable.";
    };

    vramGiB = lib.mkOption {
      type    = lib.types.float;
      default = 0.0;
      description = ''
        Total GPU/unified memory available to the model in GiB.
        Set to 0 (the default) to attempt auto-detection via nvidia-smi, AMD sysfs,
        or Apple sysctl. On Linux systems with dynamically allocated unified memory
        (e.g. AMD APU with TTM pages_limit), auto-detection reads the hardware total
        rather than the active allocation limit, so you should set this manually.
      '';
    };

    warnVramPercent = lib.mkOption {
      type    = lib.types.float;
      default = 80.0;
      description = "Quant tiles whose total size exceeds this percentage of vramGiB (or detected VRAM) are highlighted with a warning. Set to 0 to disable.";
    };

    llamaSwapConfig = lib.mkOption {
      type    = lib.types.str;
      default = "";
      description = ''
        Path to the llama-swap config.yaml file to keep in sync with downloads
        and deletes. When set, w84ggufman writes model entries to this file
        whenever a model is downloaded or deleted; llama-swap reloads it
        automatically. The file must be writable by serviceUser or serviceGroup.
        Leave empty (the default) to disable llama-swap config management.
      '';
    };

    llamaServerLandingPage = lib.mkOption {
      type    = lib.types.str;
      default = "/";
      description = ''
        URL path appended to llamaServerURL when the "Open server" button is clicked.
        Defaults to "/" (server root). Set this to the specific path of your server's
        web UI if it is not at the root. Example: "/ui/#/models" for llama-swap.
      '';
    };

    atopwebURL = lib.mkOption {
      type    = lib.types.str;
      default = "http://localhost:5899";
      description = ''
        Base URL of an atopweb instance to use for GPU VRAM monitoring.
        When reachable, w84ggufman queries <atopwebURL>/api/vram on every
        status poll instead of using system-level detection (nvidia-smi, sysfs,
        etc.). Clicking the VRAM meter in the UI opens this URL in a new tab.
        Set to "" to disable atopweb integration and always use system detection.

        When services.atopweb.enable is true the URL is derived automatically
        from that service's listen address; you do not need to set this manually.
      '';
    };

    serviceUser = lib.mkOption {
      type    = lib.types.str;
      default = "w84ggufman";
      description = "OS user the w84ggufman service runs as.";
    };

    serviceGroup = lib.mkOption {
      type    = lib.types.str;
      default = "llm";
      description = "OS group the w84ggufman service runs as. Must have write access to modelsDir.";
    };

    llamaServiceUser = lib.mkOption {
      type    = lib.types.nullOr lib.types.str;
      default = null;
      description = ''
        If set, the llama.cpp systemd service (llamaService) will be configured
        to run as this user. Setting this to the same value as serviceUser is an
        alternative to grantVRAMMonitoringCapability: when the two services
        share a UID, no capability is needed for w84ggufman to read
        /proc/<pid>/fdinfo for the llama.cpp process. Ignored if llama.cpp
        already runs as w84ggufman's user or as root.

        Example: llamaServiceUser = config.services.w84ggufman.serviceUser;
      '';
    };

    grantVRAMMonitoringCapability = lib.mkOption {
      type    = lib.types.bool;
      default = true;
      description = ''
        When true (the default) the w84ggufman service is granted
        CAP_SYS_PTRACE, which is required to read /proc/<pid>/fdinfo for
        processes owned by other users (the method used to monitor AMD GPU
        VRAM usage via fdinfo, matching nvtop). Without this capability, and
        without aligning the UIDs via llamaServiceUser, the VRAM used bar
        will show "?" on AMD systems because Linux denies cross-UID fdinfo
        reads unless the caller holds CAP_SYS_PTRACE.

        Set to false to disable VRAM usage monitoring (e.g. if you prefer to
        align UIDs via llamaServiceUser, or use nvidia-smi which does not
        need this capability).
      '';
    };
  };

  config = lib.mkMerge [
  # Auto-derive atopwebURL from services.atopweb when that service is present
  # and enabled. mkDefault lets the user override it explicitly if needed.
  (lib.mkIf (config.services ? atopweb && config.services.atopweb.enable) {
    services.w84ggufman.atopwebURL = lib.mkDefault
      "http://localhost:${toString config.services.atopweb.port}";
  })

  (lib.mkIf cfg.enable {
    # Create the service user when using the default name.
    # If serviceUser is set to an existing user, manage it yourself.
    users.users.${cfg.serviceUser} = lib.mkIf (cfg.serviceUser == "w84ggufman") {
      isSystemUser = true;
      group        = cfg.serviceGroup;
      description  = "w84ggufman service user";
    };

    systemd.services.w84ggufman = {
      description = "w84ggufman — local GGUF model management UI";
      wants       = [ "network-online.target" ];
      after       = [ "network-online.target" cfg.llamaService ];
      wantedBy    = [ "multi-user.target" ];

      path = [ pkgs.python3Packages.huggingface-hub ];

      environment = {
        # lib.mkDefault allows override in configuration.nix without a conflict error.
        HF_HOME = lib.mkDefault cfg.hfHome;
      };

      serviceConfig = {
        ExecStart  = "${cfg.package}/bin/w84ggufman --config ${configFile}";
        User       = cfg.serviceUser;
        Group      = cfg.serviceGroup;
        Restart    = "on-failure";
        RestartSec = "5s";
        UMask      = "0002";
      } // lib.optionalAttrs cfg.grantVRAMMonitoringCapability {
        # CAP_SYS_PTRACE lets the non-root service read /proc/<pid>/fdinfo for
        # processes owned by other users, which is required for AMD VRAM usage
        # monitoring via fdinfo. Both AmbientCapabilities (passed to the process)
        # and CapabilityBoundingSet (upper bound) are needed; without the latter
        # systemd would strip the capability on exec.
        AmbientCapabilities  = [ "CAP_SYS_PTRACE" ];
        CapabilityBoundingSet = [ "CAP_SYS_PTRACE" ];
      };
    };

    # When llamaServiceUser is set, run llama.cpp as that user so that w84ggufman
    # (sharing the same UID) can read /proc/<pid>/fdinfo for GPU VRAM monitoring.
    systemd.services.${cfg.llamaService} = lib.mkIf (cfg.llamaServiceUser != null) {
      serviceConfig.User = cfg.llamaServiceUser;
    };

    # Create hfHome directory and recursively fix any existing files that were
    # created with wrong permissions (e.g. from a previous run without UMask=0002).
    # 'd' creates if missing; 'Z' recursively chowns and chmods existing contents.
    systemd.tmpfiles.rules = [
      "d ${cfg.hfHome} 0775 ${cfg.serviceUser} ${cfg.serviceGroup} -"
      "Z ${cfg.hfHome} 0775 ${cfg.serviceUser} ${cfg.serviceGroup} -"
    ];

    # Allow the service user to restart managed services (llama-server and
    # optionally w84ggufman itself) without root.
    security.polkit.extraConfig = ''
      polkit.addRule(function(action, subject) {
        if (action.id == "org.freedesktop.systemd1.manage-units" &&
            ${allowedUnitsJS}.indexOf(action.lookup("unit")) >= 0 &&
            subject.user == "${cfg.serviceUser}") {
          return polkit.Result.YES;
        }
      });
    '';
  })
  ]; # mkMerge
}
