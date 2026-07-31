package config

import (
	"strings"
	"testing"
	"time"
)

func validTestConfig() Config {
	return Config{
		JWTSecret:         strings.Repeat("j", 32),
		SetupSecret:       strings.Repeat("s", 32),
		UploadMaxBytes:    1024,
		UploadQuotaBytes:  4096,
		SearchMaxPageSize: 100,
		LoginMaxFails:     5,
		SessionTTL:        time.Hour,
		TrashRetention:    10 * 24 * time.Hour,
	}
}

func TestUploadQuotaValidation(t *testing.T) {
	cfg := validTestConfig()
	cfg.UploadQuotaBytes = cfg.UploadMaxBytes - 1
	if err := cfg.validate(); err == nil {
		t.Fatal("quota smaller than the maximum file size should be rejected")
	}
}

func TestTrashRetentionValidation(t *testing.T) {
	for _, retention := range []time.Duration{0, 366 * 24 * time.Hour} {
		cfg := validTestConfig()
		cfg.TrashRetention = retention
		if err := cfg.validate(); err == nil {
			t.Fatalf("retention %s should be rejected", retention)
		}
	}
	cfg := validTestConfig()
	if err := cfg.validate(); err != nil {
		t.Fatalf("default retention should be valid: %v", err)
	}
}

func TestProductionRequiresHTTPS(t *testing.T) {
	tests := []struct {
		name         string
		baseURL      string
		cookieSecure bool
		wantErr      bool
	}{
		{name: "https with secure cookie", baseURL: "https://papers.example.com", cookieSecure: true},
		{name: "http URL", baseURL: "http://papers.example.com", cookieSecure: true, wantErr: true},
		{name: "secure cookie disabled", baseURL: "https://papers.example.com", cookieSecure: false, wantErr: true},
		{name: "relative URL", baseURL: "/papers", cookieSecure: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.Env = "production"
			cfg.PublicBaseURL = tt.baseURL
			cfg.CookieSecure = tt.cookieSecure
			if err := cfg.validate(); (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
