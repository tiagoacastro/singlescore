// Package config loads SingleScore backend configuration from environment
// variables with safe defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration.
type Config struct {
	HTTPAddr     string
	LogLevel     string
	CORSOrigins  []string
	BuildVersion string

	DB DBConfig
}

// DBConfig holds SingleStoreDB connection settings.
type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// Load reads configuration from the environment.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:     getEnv("BACKEND_HTTP_ADDR", ":8080"),
		LogLevel:     getEnv("BACKEND_LOG_LEVEL", "info"),
		CORSOrigins:  splitAndTrim(getEnv("BACKEND_CORS_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000")),
		BuildVersion: getEnv("BACKEND_BUILD_VERSION", "dev"),
	}

	port, err := strconv.Atoi(getEnv("SINGLESTORE_PORT", "3306"))
	if err != nil {
		return cfg, fmt.Errorf("invalid SINGLESTORE_PORT: %w", err)
	}

	cfg.DB = DBConfig{
		Host:     os.Getenv("SINGLESTORE_HOST"),
		Port:     port,
		User:     os.Getenv("SINGLESTORE_USER"),
		Password: os.Getenv("SINGLESTORE_PASSWORD"),
		Database: getEnv("SINGLESTORE_DATABASE", "singlescore"),
	}

	if cfg.DB.Host == "" || cfg.DB.User == "" {
		return cfg, errors.New("SINGLESTORE_HOST and SINGLESTORE_USER must be set")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func splitAndTrim(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
