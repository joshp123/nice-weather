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
    echo "Commands: go test ./..., npm run build, go run ./cmd/niceweather fixture --out ./artifacts, go run ./cmd/niceweather update --cache ./.cache/weather --out ./artifacts --smoke, go run ./cmd/niceweather serve --web ./dist --artifacts ./artifacts --listen 127.0.0.1:5198"
  '';
}
