package server

import (
	"fmt"
	"net/http"
)

func Serve(dir, addr string) error {
	handler := http.FileServer(http.Dir(dir))
	fmt.Printf("serving %s at http://%s\n", dir, addr)
	return http.ListenAndServe(addr, handler)
}
