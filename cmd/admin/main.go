package main

import (
	"log"
	"os"
	"strings"

	"github.com/app-plat/app-plat/internal/adminui"
)

func main() {
	addr := getenv("LISTEN_ADDR", ":8081")
	cp := getenv("CONTROL_PLANE_URL", "http://control-plane:8080")
	if err := adminui.ListenAndServe(addr, cp); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
