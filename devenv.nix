{ pkgs, ... }:

{
  packages = [
    pkgs.go
    pkgs.nodejs_24
    pkgs.gdal
    pkgs.eccodes
    pkgs.imagemagick
  ];

  enterShell = ''
    echo "Nice Weather Finder dev shell"
    echo "Commands: go test ./..., go run ./cmd/weatherchase build --dir ./.cache/dwd/daytime --out ./public/artifacts --run 'YYYYMMDDHH steps 008-018', npm run dev"
  '';
}
