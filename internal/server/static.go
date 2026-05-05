package server

import (
	"fmt"
	"net/http"

	"nice-weather-finder/internal/artifact"
)

func Handler(webDir, artifactDir string) http.Handler {
	mux := http.NewServeMux()
	artifactRoot := artifact.PublishedRoot(artifactDir)
	mux.Handle("/artifacts/", http.StripPrefix("/artifacts/", http.FileServer(http.Dir(artifactRoot))))
	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	return mux
}

func Serve(webDir, artifactDir, addr string) error {
	fmt.Printf("serving web %s and artifacts %s at http://%s\n", webDir, artifact.PublishedRoot(artifactDir), addr)
	return http.ListenAndServe(addr, Handler(webDir, artifactDir))
}
