package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Server     ServerConfig
	JWT        JWTConfig
	Rate       RateLimitConfig
	Health     HealthConfig
	Kubernetes KubernetesConfig
	Logging    LoggingConfig
}

// LoggingConfig holds logging-related configuration
type LoggingConfig struct {
	Level       string `yaml:"level" json:"level"`
	Format      string `yaml:"format" json:"format"`
	Output      string `yaml:"output" json:"output"`
	EnableHooks bool   `yaml:"enable_hooks" json:"enable_hooks"`

	// Error tracking configuration
	ErrorWebhookURL string        `yaml:"error_webhook_url" json:"error_webhook_url"`
	SlackWebhookURL string        `yaml:"slack_webhook_url" json:"slack_webhook_url"`
	AlertCooldown   time.Duration `yaml:"alert_cooldown" json:"alert_cooldown"`

	// Request logging configuration
	LogRequests          bool          `yaml:"log_requests" json:"log_requests"`
	LogResponses         bool          `yaml:"log_responses" json:"log_responses"`
	LogHeaders           bool          `yaml:"log_headers" json:"log_headers"`
	SensitiveHeaders     []string      `yaml:"sensitive_headers" json:"sensitive_headers"`
	SlowRequestThreshold time.Duration `yaml:"slow_request_threshold" json:"slow_request_threshold"`

	// Loki
	LokiURL string `yaml:"loki_url" json:"loki_url"`
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
		Logging: LoggingConfig{
			Level:                getEnv("LOG_LEVEL", "info"),
			Format:               getEnv("LOG_FORMAT", "json"),
			Output:               getEnv("LOG_OUTPUT", "stdout"),
			EnableHooks:          getEnvAsBool("LOG_ENABLE_HOOKS", true),
			ErrorWebhookURL:      getEnv("ERROR_WEBHOOK_URL", ""),
			SlackWebhookURL:      getEnv("SLACK_WEBHOOK_URL", ""),
			AlertCooldown:        getEnvAsDuration("ALERT_COOLDOWN", 5*time.Minute),
			LogRequests:          getEnvAsBool("LOG_REQUESTS", true),
			LogResponses:         getEnvAsBool("LOG_RESPONSES", false),
			LogHeaders:           getEnvAsBool("LOG_HEADERS", false),
			SensitiveHeaders:     getEnvAsStringSlice("SENSITIVE_HEADERS", []string{"authorization", "cookie", "x-api-key"}),
			SlowRequestThreshold: getEnvAsDuration("SLOW_REQUEST_THRESHOLD", 5*time.Second),
			LokiURL:              getEnv("LOG_LOKI_URL", ""),
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

	validLevels := map[string]bool{
		"debug": true, "info": true, "warn": true, "error": true, "fatal": true,
	}
	if !validLevels[c.Logging.Level] {
		return errors.New("LOG_LEVEL must be one of: debug, info, warn, error, fatal")
	}

	validFormats := map[string]bool{
		"json": true, "text": true,
	}
	if !validFormats[c.Logging.Format] {
		return errors.New("LOG_FORMAT must be one of: json, text")
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

func getEnvAsStringSlice(key string, fallback []string) []string {
	valStr := getEnv(key, "")
	if valStr == "" {
		return fallback
	}

	// Split by comma and trim spaces
	result := make([]string, 0)
	for _, item := range strings.Split(valStr, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	if len(result) == 0 {
		return fallback
	}

	return result
}
