package main

import (
	"api-gateway/internal/config"
	"api-gateway/internal/router"
	"log"
)

func main() {
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	router.Setup(cfg)
}
