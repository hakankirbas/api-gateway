package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Server     ServerConfig
	JWT        JWTConfig
	Rate       RateLimitConfig
	Health     HealthConfig
	Kubernetes KubernetesConfig
}

type ServerConfig struct {
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type JWTConfig struct {
	Secret     string
	Expiration time.Duration
}

type RateLimitConfig struct {
	Limit           int
	BurstLimit      int
	CleanupInterval time.Duration
}

type HealthConfig struct {
	CheckInterval time.Duration
	Timeout       time.Duration
}

type KubernetesConfig struct {
	Enabled            bool
	Namespace          string
	InCluster          bool
	KubeconfigPath     string
	ServiceDiscovery   bool
	WatchAllNamespaces bool
}

func Load() *Config {
	godotenv.Load()

	return &Config{
		Server: ServerConfig{
			Port:         getEnv("PORT", ":8080"),
			ReadTimeout:  getEnvAsDuration("READ_TIMEOUT", 30*time.Second),
			WriteTimeout: getEnvAsDuration("WRITE_TIMEOUT", 30*time.Second),
		},
		JWT: JWTConfig{
			Secret:     getEnv("JWT_SECRET", "supersecret"),
			Expiration: getEnvAsDuration("JWT_EXPIRATION", 24*time.Hour),
		},
		Rate: RateLimitConfig{
			Limit:           getEnvAsInt("RATE_LIMIT", 1),
			BurstLimit:      getEnvAsInt("RATE_BURST_LIMIT", 5),
			CleanupInterval: getEnvAsDuration("RATE_CLEANUP", 1*time.Minute),
		},
		Health: HealthConfig{
			CheckInterval: getEnvAsDuration("HEALTH_CHECK_INTERVAL", 10*time.Second),
			Timeout:       getEnvAsDuration("HEALTH_CHECK_TIMEOUT", 5*time.Second),
		},
		Kubernetes: KubernetesConfig{
			Enabled:            getEnvAsBool("KUBERNETES_ENABLED", true),
			Namespace:          getEnv("KUBERNETES_NAMESPACE", "api-gateway"),
			InCluster:          getEnvAsBool("KUBERNETES_IN_CLUSTER", true),
			KubeconfigPath:     getEnv("KUBECONFIG_PATH", ""),
			ServiceDiscovery:   getEnvAsBool("KUBERNETES_SERVICE_DISCOVERY", true),
			WatchAllNamespaces: getEnvAsBool("KUBERNETES_WATCH_ALL_NAMESPACES", false),
		},
	}
}

func (c *Config) Validate() error {
	if c.JWT.Secret == "supersecret" {
		return errors.New("JWT_SECRET must be changed from default value")
	}
	if c.Rate.Limit <= 0 {
		return errors.New("RATE_LIMIT must be positive")
	}
	if c.Rate.BurstLimit <= 0 {
		return errors.New("RATE_BURST_LIMIT must be positive")
	}
	if c.Kubernetes.Enabled && c.Kubernetes.Namespace == "" {
		return errors.New("KUBERNETES_NAMESPACE must be set when Kubernetes is enabled")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	valStr := getEnv(key, "")
	if valStr == "" {
		return fallback
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return fallback
	}
	return val
}

func getEnvAsBool(key string, fallback bool) bool {
	valStr := getEnv(key, "")
	if valStr == "" {
		return fallback
	}
	val, err := strconv.ParseBool(valStr)
	if err != nil {
		return fallback
	}
	return val
}

func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	valStr := getEnv(key, "")
	if valStr == "" {
		return fallback
	}
	val, err := time.ParseDuration(valStr)
	if err != nil {
		return fallback
	}
	return val
}
