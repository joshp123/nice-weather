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
    echo "Commands: go test ./..., npm run build, go run ./cmd/niceweather forecast-world --help, go run ./cmd/niceweather climate-world --help, go run ./cmd/niceweather verify-climate --out ./artifacts, go run ./cmd/niceweather serve --web ./dist --artifacts ./artifacts --listen 127.0.0.1:5198"
  '';
}
