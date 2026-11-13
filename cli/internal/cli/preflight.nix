# What `piste switch` reads of a NixOS host's new configuration before it
# activates it (preflight.go): whether the invoking user keeps SSH (root its
# own login) and sudo, and whether the machine still boots, its own root
# filesystem included.
# Applied as `(<this file>) "<user>"` to nixosConfigurations.<host>.config, so
# it evaluates only the options it names, never the system's build.
#
# One line in the eval's argv: comments only on lines of their own.
user: config:
let
  inherit (builtins) any elem length;
  u = config.users.users.${user} or null;
  ssh = config.services.openssh;
  sudo = config.security.sudo;
  loader = config.boot.loader;
  root = config.fileSystems."/" or null;
  forUser = r: elem user r.users || elem "wheel" r.groups;
  # NOPASSWD for every command: one for systemctl alone still leaves
  # nixos-rebuild, rollback and the undo asking a password.
  nopasswd = c: builtins.isAttrs c && c.command == "ALL" && elem "NOPASSWD" c.options;
  passwords = [ "hashedPassword" "hashedPasswordFile" "password" "initialPassword" "initialHashedPassword" ];
in
{
  SSHEnabled = ssh.enable;
  PasswordAuth = ssh.settings.PasswordAuthentication == true;
  KbdInteractive = ssh.settings.KbdInteractiveAuthentication == true;
  KeysInHomedir = ssh.authorizedKeysInHomedir or true;
  Keys = if u == null then 0 else length u.openssh.authorizedKeys.keys;
  KeyFiles = if u == null then 0 else length u.openssh.authorizedKeys.keyFiles;
  # null leaves sshd's own default (prohibit-password): read as "".
  PermitRootLogin = if ssh.settings.PermitRootLogin == null then "" else ssh.settings.PermitRootLogin;
  Wheel = (u != null && (elem "wheel" u.extraGroups || u.group == "wheel")) || elem user (config.users.groups.wheel.members or [ ]);
  SudoEnabled = sudo.enable;
  WheelNeedsPassword = sudo.wheelNeedsPassword;
  NopasswdForUser = any (r: forUser r && any nopasswd r.commands) sudo.extraRules;
  HasPassword = u != null && any (o: u.${o} != null) passwords;
  BootLoader = loader.systemd-boot.enable || (loader.grub.enable && (loader.grub.devices != [ ] || loader.grub.mirroredBoots != [ ])) || loader.generic-extlinux-compatible.enable || (loader.external.enable or false) || (loader.limine.enable or false) || (loader.refind.enable or false);
  IsContainer = config.boot.isContainer;
  RootDevice = if root == null || root.device == null then "" else root.device;
}
