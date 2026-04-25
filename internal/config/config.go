package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                 string
	DatabaseURL          string
	DirectURL            string // used only for migrations (bypasses pgbouncer)
	RedisURL             string
	ShopifyAPIKey        string
	ShopifyAPISecret     string
	ShopifyWebhookSecret string
	EncryptionKey        string
	AppURL               string
	Environment          string
	JWTSecret            string
	FrontendURL          string
	WPJWTSecret          string
	WPPluginVersion      string // e.g. "1.0.9" — served by GET /api/wp/plugin/version
	WPPluginDownloadURL  string // S3/GitHub URL for the plugin zip
}

func Load() (*Config, error) {
	// Load .env file if present — silently ignored in production where real
	// environment variables are injected by the platform (Render, etc.).
	_ = godotenv.Load(".env")

	cfg := &Config{
		Port:                 getEnv("PORT", "3000"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		DirectURL:            os.Getenv("DIRECT_URL"),
		RedisURL:             os.Getenv("REDIS_URL"),
		ShopifyAPIKey:        os.Getenv("SHOPIFY_API_KEY"),
		ShopifyAPISecret:     os.Getenv("SHOPIFY_API_SECRET"),
		ShopifyWebhookSecret: os.Getenv("SHOPIFY_WEBHOOK_SECRET"),
		EncryptionKey:        os.Getenv("ENCRYPTION_KEY"),
		JWTSecret:            os.Getenv("JWT_SECRET"),
		WPJWTSecret:          os.Getenv("WP_JWT_SECRET"),
		WPPluginVersion:      getEnv("WP_PLUGIN_VERSION", "1.0.0"),
		WPPluginDownloadURL:  os.Getenv("WP_PLUGIN_DOWNLOAD_URL"),
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
	if cfg.ShopifyAPIKey == "" {
		return nil, fmt.Errorf("SHOPIFY_API_KEY is required")
	}
	if cfg.ShopifyAPISecret == "" {
		return nil, fmt.Errorf("SHOPIFY_API_SECRET is required")
	}

	return cfg, nil
}

// WPJWTSecretWarning returns a non-nil error if WP_JWT_SECRET is missing.
// Callers should Fatal if WordPress merchants exist; Warn otherwise.
func (c *Config) WPJWTSecretWarning() error {
	if c.WPJWTSecret == "" {
		return fmt.Errorf("WP_JWT_SECRET is not set — WordPress merchant auth will not work")
	}
	return nil
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
