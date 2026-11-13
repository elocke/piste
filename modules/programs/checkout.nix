# Where the piste checkout lives on this machine: a
# clone of the engine, with the overlays in overlays/<name>/. The agent skill
# links point into it (out of the store, so edits are live), and pisteup and
# nixconf work on it.
{ config, lib, ... }:
{
  options.piste.checkout = lib.mkOption {
    type = lib.types.str;
    default = "${config.home.homeDirectory}/piste";
    defaultText = lib.literalExpression ''"''${config.home.homeDirectory}/piste"'';
    description = "The piste checkout on this machine. Agent skill links, pisteup and nixconf point into it.";
  };
}
