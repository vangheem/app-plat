package main

import (
	"log"
	"net/http"
	"strings"

	"github.com/app-plat/app-plat/internal/llmgateway"
)

func main() {
	cfg := llmgateway.ConfigFromEnv()
	if cfg.InternalToken == "" {
		log.Fatal("INTERNAL_TOKEN is required")
	}
	if strings.ContainsAny(cfg.InternalToken, "\r\n") {
		log.Fatal("INTERNAL_TOKEN contains invalid characters")
	}
	if cfg.OpenRouterAPIKey == "" {
		log.Print("warning: OPENROUTER_API_KEY is empty; chat completions will fail")
	}
	log.Printf("llm-gateway listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, llmgateway.New(cfg).Handler()); err != nil {
		log.Fatal(err)
	}
}
