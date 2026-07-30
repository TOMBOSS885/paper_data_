package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env               string
	Host              string
	Port              string
	PublicBaseURL     string
	MySQLDSN          string
	JWTSecret         string
	SetupSecret       string
	CookieSecure      bool
	CookieSameSite    string
	AllowedOrigins    map[string]struct{}
	UploadDir         string
	UploadMaxBytes    int64
	UploadQuotaBytes  int64
	SearchMaxPageSize int
	LoginMaxFails     int
	LoginWindow       time.Duration
	SessionTTL        time.Duration
	TrashRetention    time.Duration
	AutoMigrate       bool
}

func Load() (Config, error) {
	c := Config{
		Env:               getenv("APP_ENV", "development"),
		Host:              getenv("SERVER_HOST", "0.0.0.0"),
		Port:              getenv("SERVER_PORT", "8080"),
		PublicBaseURL:     getenv("PUBLIC_BASE_URL", "http://localhost:8080"),
		JWTSecret:         os.Getenv("JWT_SECRET"),
		SetupSecret:       os.Getenv("SETUP_SECRET"),
		CookieSecure:      envBool("COOKIE_SECURE", false),
		CookieSameSite:    getenv("COOKIE_SAMESITE", "lax"),
		AllowedOrigins:    splitSet(os.Getenv("CORS_ALLOWED_ORIGINS")),
		UploadDir:         getenv("UPLOAD_DIR", "./uploads"),
		UploadMaxBytes:    envInt64("UPLOAD_MAX_BYTES", 200*1024*1024),
		UploadQuotaBytes:  envInt64("UPLOAD_QUOTA_BYTES", 100*1024*1024*1024),
		SearchMaxPageSize: envInt("SEARCH_MAX_PAGE_SIZE", 100),
		LoginMaxFails:     envInt("LOGIN_LIMIT_MAX_FAILS", 5),
		LoginWindow:       time.Duration(envInt("LOGIN_LIMIT_WINDOW_SECONDS", 600)) * time.Second,
		SessionTTL:        time.Duration(envInt("SESSION_TTL_SECONDS", 12*60*60)) * time.Second,
		TrashRetention:    time.Duration(envInt("TRASH_RETENTION_DAYS", 10)) * 24 * time.Hour,
		AutoMigrate:       envBool("AUTO_MIGRATE", true),
	}
	c.MySQLDSN = mysqlDSN()
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() error {
	if len(c.JWTSecret) < 32 {
		return errors.New("JWT_SECRET must contain at least 32 bytes")
	}
	if len(c.SetupSecret) < 32 {
		return errors.New("SETUP_SECRET must contain at least 32 bytes")
	}
	if c.Env == "production" && c.SetupSecret == c.JWTSecret {
		return errors.New("SETUP_SECRET must be a separate random secret in production")
	}
	if c.SearchMaxPageSize < 1 || c.SearchMaxPageSize > 100 {
		return errors.New("SEARCH_MAX_PAGE_SIZE must be between 1 and 100")
	}
	if c.LoginMaxFails < 1 {
		return errors.New("LOGIN_LIMIT_MAX_FAILS must be at least 1")
	}
	if c.SessionTTL < time.Minute {
		return errors.New("SESSION_TTL_SECONDS must be at least 60")
	}
	if c.TrashRetention < 24*time.Hour || c.TrashRetention > 365*24*time.Hour {
		return errors.New("TRASH_RETENTION_DAYS must be between 1 and 365")
	}
	return nil
}

func mysqlDSN() string {
	if dsn := os.Getenv("MYSQL_DSN"); dsn != "" {
		return dsn
	}
	host := getenv("MYSQL_HOST", "127.0.0.1")
	port := getenv("MYSQL_PORT", "3306")
	db := getenv("MYSQL_DATABASE", "paper_kb")
	user := getenv("MYSQL_USERNAME", "paper_kb_app")
	password := os.Getenv("MYSQL_PASSWORD")
	tlsMode := getenv("MYSQL_TLS_MODE", "false")
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true&loc=UTC&tls=%s&multiStatements=false", user, password, host, port, db, tlsMode)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envInt(key string, fallback int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return n
}

func envInt64(key string, fallback int64) int64 {
	n, err := strconv.ParseInt(os.Getenv(key), 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func splitSet(v string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, item := range strings.Split(v, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out[item] = struct{}{}
		}
	}
	return out
}
