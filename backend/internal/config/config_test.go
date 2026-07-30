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
		SearchMaxPageSize: 100,
		LoginMaxFails:     5,
		SessionTTL:        time.Hour,
		TrashRetention:    10 * 24 * time.Hour,
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
