package main

import (
	"log"
	"os"
	"strings"

	"github.com/app-plat/app-plat/internal/userui"
)

func main() {
	addr := getenv("LISTEN_ADDR", ":8082")
	cp := getenv("CONTROL_PLANE_URL", "http://control-plane:8080")
	if err := userui.ListenAndServe(addr, cp); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
