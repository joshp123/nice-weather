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
    echo "Commands: go test ./..., go run ./cmd/weatherchase build --dir ./.cache/dwd/highres --out ./public/artifacts --run 'YYYYMMDDHH step 000', npm run dev"
  '';
}
