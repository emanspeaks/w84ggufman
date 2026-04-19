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
  };

  config = lib.mkIf cfg.enable {
    # Create the service user when using the default name.
    # If serviceUser is set to an existing user, manage it yourself.
    users.users.${cfg.serviceUser} = lib.mkIf (cfg.serviceUser == "w84ggufman") {
      isSystemUser = true;
      group        = cfg.serviceGroup;
      # 'render' group gives access to /dev/dri/renderD* for AMD VRAM monitoring
      # via the AMDGPU_INFO_MEMORY DRM ioctl. Safe to include on non-AMD systems.
      extraGroups  = [ "render" ];
      description  = "w84ggufman service user";
    };

    systemd.services.w84ggufman = {
      description = "w84ggufman — local GGUF model management UI";
      after       = [ "network.target" cfg.llamaService ];
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
        # Grant render-group access at runtime for AMD VRAM ioctl, even when
        # running as a pre-existing user not in the render group.
        SupplementaryGroups = "render";
        Restart    = "on-failure";
        RestartSec = "5s";
        UMask      = "0002";
      };
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
  };
}
