package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Server ServerConfig
	JWT    JWTConfig
	Rate   RateLimitConfig
	Health HealthConfig
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
