{ pkgs, ... }:

{
  packages = [
    pkgs.go
    pkgs.nodejs_24
  ];

  enterShell = ''
    echo "Nice Weather Finder: npm run build && npm run serve  ->  http://127.0.0.1:5198"
  '';
}
