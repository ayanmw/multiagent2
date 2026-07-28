package main

import (
	"flag"
	"log"
	"net/http"

	"workbuddyllmapi/internal/backend"
	"workbuddyllmapi/internal/config"
	"workbuddyllmapi/internal/openai"
)

func main() {
	cfg := config.Load()
	flag.StringVar(&cfg.ListenAddr, "addr", cfg.ListenAddr, "listen address, e.g. :8080")
	flag.StringVar(&cfg.Backend, "backend", cfg.Backend, "backend: passthrough|mock|codebuddy")
	flag.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "passthrough upstream base URL")
	flag.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "passthrough upstream API key")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("[workbuddyLLMAPI] backend=%s listen=%s", cfg.Backend, cfg.ListenAddr)

	b, err := backend.New(cfg)
	if err != nil {
		log.Fatalf("[workbuddyLLMAPI] init backend: %v", err)
	}

	mux := openai.NewServer(b, cfg)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("[workbuddyLLMAPI] server: %v", err)
	}
}
