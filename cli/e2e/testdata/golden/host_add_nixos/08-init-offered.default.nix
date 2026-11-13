# box (NixOS): this machine's own settings. What's yours on every
# machine is in ../../home.nix; the engine's NixOS platform module brings the
# user, sudo, Nix settings and home-manager, and the roles in flake.nix the
# shell, git, ssh, editor and tmux. The machine itself (disks, boot loader,
# hardware) is in machine/configuration.nix.
{ user, pkgs, ... }:
{
  imports = [ ./machine/configuration.nix ];

  # Who can log in over SSH as jane: public keys, one string each.
  users.users.${user}.openssh.authorizedKeys.keys = [ "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyOnlyForTests jane@example.com" ];

  # This machine's own packages: add them to this list by hand (`piste adopt`
  # doesn't write to a NixOS host yet).
  piste.packages.extraPackages = with pkgs; [ ];

  home-manager.users.${user} =
    { config, ... }:
    {
      imports = [ ../../home.nix ];

      # The home-manager release this machine started at; leave it as it is
      # (home-manager's news says when a change is worth bumping it for).
      home.stateVersion = "26.11";

      # Where the piste checkout is on this machine; agent skill links point
      # into it, so an edit to a SKILL.md there is live without a switch.
      piste.checkout = "$WORK/ck2";
    };
}
