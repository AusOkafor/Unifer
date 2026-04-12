package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                 string
	DatabaseURL          string
	RedisURL             string
	ShopifyAPIKey        string
	ShopifyAPISecret     string
	ShopifyWebhookSecret string
	EncryptionKey        string
	AppURL               string
	Environment          string
	JWTSecret            string
	FrontendURL          string
}

func Load() (*Config, error) {
	// Load .env file if present — silently ignored in production where real
	// environment variables are injected by the platform (Render, etc.).
	_ = godotenv.Load(".env")

	cfg := &Config{
		Port:                 getEnv("PORT", "3000"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		RedisURL:             os.Getenv("REDIS_URL"),
		ShopifyAPIKey:        os.Getenv("SHOPIFY_API_KEY"),
		ShopifyAPISecret:     os.Getenv("SHOPIFY_API_SECRET"),
		ShopifyWebhookSecret: os.Getenv("SHOPIFY_WEBHOOK_SECRET"),
		EncryptionKey:        os.Getenv("ENCRYPTION_KEY"),
		JWTSecret:            os.Getenv("JWT_SECRET"),
		AppURL:               os.Getenv("APP_URL"),
		Environment:          getEnv("ENVIRONMENT", "development"),
		FrontendURL:          getEnv("FRONTEND_URL", "http://localhost:8080"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}
	if cfg.EncryptionKey == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}

	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
