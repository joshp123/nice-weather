package server

import (
	"fmt"
	"net/http"
)

func Handler(webDir, artifactDir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/artifacts/", http.StripPrefix("/artifacts/", http.FileServer(http.Dir(artifactDir))))
	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	return mux
}

func Serve(webDir, artifactDir, addr string) error {
	fmt.Printf("serving web %s and artifacts %s at http://%s\n", webDir, artifactDir, addr)
	return http.ListenAndServe(addr, Handler(webDir, artifactDir))
}
