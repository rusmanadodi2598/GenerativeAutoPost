// Package config loads typed process settings once at boot.
//
// @file      internal/config/config.go
// @for       Typed environment config validated at boot.
// @uses      os, go-playground/validator
// @reason    Single place where raw env enters the process.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     config
// @stability stable
// @since     2026-10-12
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
)

// AppEnv values gate debug detail in error responses.
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// Drivers selects the media Store implementation.
const (
	DriverDisk = "disk"
	DriverS3   = "s3"
)

// Config holds every section 11 variable in typed form.
type Config struct {
	AppEnv             string        `validate:"required,oneof=development production"`
	AppPort            int           `validate:"required,min=1,max=65535"`
	DatabaseURL        string        `validate:"required"`
	DBMaxOpenConns     int32         `validate:"gte=0"`
	DBMaxIdleConns     int32         `validate:"gte=0"`
	DBConnMaxLifetime  time.Duration `validate:"gte=0"`
	RedisURL           string        `validate:"required"`
	TikTokClientKey    string        `validate:"required"`
	TikTokClientSecret string
	TikTokRedirectURI  string `validate:"required"`
	TikTokScopes       string `validate:"required"`
	TokenEncKey        []byte `validate:"required,len=32"`
	TokenEncKeyID      string `validate:"required"`
	MediaDriver        string `validate:"required,oneof=disk s3"`
	MediaPublicBaseURL string
	S3Endpoint         string
	S3Bucket           string
	S3AccessKey        string
	S3SecretKey        string
	S3Region           string
	SessionCookieName  string        `validate:"required"`
	SessionTTL         time.Duration `validate:"required,gt=0"`
	LogLevel           string        `validate:"required"`
}

var validate = validator.New()

// getenv reads one variable; only this file touches os.Getenv.
func getenv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// intOr parses an optional integer env, falling back to def.
func intOr(key string, def int) (int, error) {
	raw := getenv(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing %s: %w", key, err)
	}
	return n, nil
}

// durationOr parses an optional duration env, falling back to def.
func durationOr(key string, def time.Duration) (time.Duration, error) {
	raw := getenv(key)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing %s: %w", key, err)
	}
	return d, nil
}

// Load reads the environment once and fails fast on bad values.
func Load() (Config, error) {
	port, err := intOr("APP_PORT", 8080)
	if err != nil {
		return Config{}, err
	}
	openConns, err := intOr("DB_MAX_OPEN_CONNS", 25)
	if err != nil {
		return Config{}, err
	}
	idleConns, err := intOr("DB_MAX_IDLE_CONNS", 5)
	if err != nil {
		return Config{}, err
	}
	maxLife, err := durationOr("DB_CONN_MAX_LIFETIME", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	sessTTL, err := durationOr("SESSION_TTL", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	name := getenv("SESSION_COOKIE_NAME")
	if name == "" {
		name = "genauto_session"
	}
	level := getenv("LOG_LEVEL")
	if level == "" {
		level = "info"
	}
	cfg := Config{
		AppEnv:             getenv("APP_ENV"),
		AppPort:            port,
		DatabaseURL:        getenv("DATABASE_URL"),
		DBMaxOpenConns:     int32(openConns),
		DBMaxIdleConns:     int32(idleConns),
		DBConnMaxLifetime:  maxLife,
		RedisURL:           getenv("REDIS_URL"),
		TikTokClientKey:    getenv("TIKTOK_CLIENT_KEY"),
		TikTokClientSecret: getenv("TIKTOK_CLIENT_SECRET"),
		TikTokRedirectURI:  getenv("TIKTOK_REDIRECT_URI"),
		TikTokScopes:       getenv("TIKTOK_SCOPES"),
		TokenEncKey:        []byte(getenv("TOKEN_ENC_KEY")),
		TokenEncKeyID:      getenv("TOKEN_ENC_KEY_ID"),
		MediaDriver:        getenv("MEDIA_DRIVER"),
		MediaPublicBaseURL: getenv("MEDIA_PUBLIC_BASE_URL"),
		S3Endpoint:         getenv("S3_ENDPOINT"),
		S3Bucket:           getenv("S3_BUCKET"),
		S3AccessKey:        getenv("S3_ACCESS_KEY"),
		S3SecretKey:        getenv("S3_SECRET_KEY"),
		S3Region:           getenv("S3_REGION"),
		SessionCookieName:  name,
		SessionTTL:         sessTTL,
		LogLevel:           level,
	}
	if cfg.AppEnv == "" {
		cfg.AppEnv = EnvDevelopment
	}
	if cfg.MediaDriver == "" {
		cfg.MediaDriver = DriverDisk
	}
	if err := validate.Struct(cfg); err != nil {
		return Config{}, fmt.Errorf("validating config: %w", err)
	}
	if cfg.MediaDriver == DriverS3 && cfg.S3Bucket == "" {
		return Config{}, errors.New("S3_BUCKET is required when MEDIA_DRIVER=s3")
	}
	return cfg, nil
}

// IsDevelopment reports whether debug detail may be exposed.
func (c Config) IsDevelopment() bool {
	return c.AppEnv == EnvDevelopment
}

// Addr returns the listen address for the HTTP server.
func (c Config) Addr() string {
	return ":" + strconv.Itoa(c.AppPort)
}
