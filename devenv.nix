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
    echo "Commands: go test ./..., go run ./cmd/weatherchase update --cache ./.cache/dwd/forecast --land ./.cache/land/ne_50m_land.geojson --out ./artifacts --days 5, go run ./cmd/weatherchase serve --web ./dist --artifacts ./artifacts, npm run dev"
  '';
}
