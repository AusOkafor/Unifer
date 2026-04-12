package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	Port                 string `mapstructure:"PORT"`
	DatabaseURL          string `mapstructure:"DATABASE_URL"`
	RedisURL             string `mapstructure:"REDIS_URL"`
	ShopifyAPIKey        string `mapstructure:"SHOPIFY_API_KEY"`
	ShopifyAPISecret     string `mapstructure:"SHOPIFY_API_SECRET"`
	ShopifyWebhookSecret string `mapstructure:"SHOPIFY_WEBHOOK_SECRET"`
	EncryptionKey        string `mapstructure:"ENCRYPTION_KEY"`
	AppURL               string `mapstructure:"APP_URL"`
	Environment          string `mapstructure:"ENVIRONMENT"`
	JWTSecret            string `mapstructure:"JWT_SECRET"`
	FrontendURL          string `mapstructure:"FRONTEND_URL"`
}

func Load() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.SetConfigType("env")
	// Read .env file if present; ignore error if missing (env vars suffice)
	_ = viper.ReadInConfig()

	viper.AutomaticEnv()

	// Defaults
	viper.SetDefault("PORT", "3000")
	viper.SetDefault("ENVIRONMENT", "development")
	viper.SetDefault("FRONTEND_URL", "http://localhost:8080")

	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
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
