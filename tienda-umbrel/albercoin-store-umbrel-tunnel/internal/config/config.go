package config

import (
	"os"
)

type Config struct {
	ListenAddr  string
	DataDir     string
	UIAuthUser  string
	UIAuthPass  string
	WGInterface string
}

func Load() *Config {
	return &Config{
		ListenAddr:  getEnv("LISTEN_ADDR", ":8080"),
		DataDir:     getEnv("DATA_DIR", "/data"),
		UIAuthUser:  getEnv("UI_AUTH_USER", "admin"),
		UIAuthPass:  getEnv("UI_AUTH_PASS", "umbrel"),
		WGInterface: getEnv("WG_INTERFACE", "umbreltun"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
