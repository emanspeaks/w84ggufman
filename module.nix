{ config, lib, pkgs, ... }:

let
  cfg = config.services.gguf-manager;
  configFile = pkgs.writeText "gguf-manager.json" (builtins.toJSON {
    modelsDir      = cfg.modelsDir;
    llamaServerURL = cfg.llamaServerURL;
    llamaService   = cfg.llamaService;
    port           = cfg.port;
    hfToken        = cfg.hfToken;
  });
in {
  options.services.gguf-manager = {
    enable = lib.mkEnableOption "gguf-manager local model manager UI";

    package = lib.mkOption {
      type        = lib.types.package;
      description = "The gguf-manager package to use.";
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

    hfToken = lib.mkOption {
      type    = lib.types.str;
      default = "";
      description = "Optional HuggingFace token for private repos or higher rate limits.";
    };

    serviceUser = lib.mkOption {
      type    = lib.types.str;
      default = "gguf-manager";
      description = "OS user the gguf-manager service runs as.";
    };

    serviceGroup = lib.mkOption {
      type    = lib.types.str;
      default = "llm";
      description = "OS group the gguf-manager service runs as. Must have write access to modelsDir.";
    };
  };

  config = lib.mkIf cfg.enable {
    # Create the service user when using the default name.
    # If serviceUser is set to an existing user, manage it yourself.
    users.users.${cfg.serviceUser} = lib.mkIf (cfg.serviceUser == "gguf-manager") {
      isSystemUser = true;
      group        = cfg.serviceGroup;
      description  = "gguf-manager service user";
    };

    systemd.services.gguf-manager = {
      description = "gguf-manager — local GGUF model management UI";
      after       = [ "network.target" cfg.llamaService ];
      wantedBy    = [ "multi-user.target" ];

      path = [ pkgs.python3Packages.huggingface-hub ];

      environment = {
        # lib.mkDefault allows override in configuration.nix without a conflict error.
        HF_HOME = lib.mkDefault cfg.hfHome;
      };

      serviceConfig = {
        ExecStart  = "${cfg.package}/bin/gguf-manager --config ${configFile}";
        User       = cfg.serviceUser;
        Group      = cfg.serviceGroup;
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

    # Allow the service user to restart the llama service without root.
    security.polkit.extraConfig = ''
      polkit.addRule(function(action, subject) {
        if (action.id == "org.freedesktop.systemd1.manage-units" &&
            action.lookup("unit") == "${cfg.llamaService}" &&
            subject.user == "${cfg.serviceUser}") {
          return polkit.Result.YES;
        }
      });
    '';
  };
}
