package main

import (
	"api-gateway/internal/config"
	"api-gateway/internal/router"
	"api-gateway/pkg/logger"
	"log"
	"os"
	"time"
)

func main() {
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	loggerConfig := logger.Config{
		Level:       cfg.Logging.Level,
		Format:      "json",
		Service:     "api-gateway",
		Output:      "stdout",
		EnableHooks: false,
	}

	testLogger := logger.NewLogger(loggerConfig)

	testLogger.Info("=== API GATEWAY STARTING ===", map[string]interface{}{
		"timestamp":   time.Now().UTC(),
		"version":     "1.0.0",
		"environment": os.Getenv("ENVIRONMENT"),
		"config": map[string]interface{}{
			"log_level":  cfg.Logging.Level,
			"log_format": "json",
			"log_output": "stdout",
		},
	})

	testLogger.Info("Configuration loaded successfully", map[string]interface{}{
		"kubernetes_enabled": cfg.Kubernetes.Enabled,
		"service_discovery":  cfg.Kubernetes.ServiceDiscovery,
		"namespace":          cfg.Kubernetes.Namespace,
	})

	router.Setup(cfg)
}
