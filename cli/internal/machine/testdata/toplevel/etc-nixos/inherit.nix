let
  hostName = "example";
in
{
  networking = {
    inherit hostName;
    useDHCP = true;
  };
}
