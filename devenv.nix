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
    echo "Commands: go test ./..., go run ./cmd/weatherchase build --dir ./.cache/dwd/daytime --land ./.cache/land/ne_50m_land.geojson --out ./artifacts --run 'YYYYMMDDHH steps 008-018', go run ./cmd/weatherchase serve --web ./dist --artifacts ./artifacts, npm run dev"
  '';
}
