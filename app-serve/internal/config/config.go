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
	"strconv"
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

// Defaults applied when the matching env is absent.
const (
	defaultChunkMaxBytes = 64 << 20
	defaultWorkerSize    = 4
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
	OperatorID         string `validate:"required,uuid"`
	MediaDriver        string `validate:"required,oneof=disk s3"`
	MediaPublicBaseURL string
	MediaRoot          string
	MediaMaxBytes      int64 `validate:"gte=0"`
	MediaMaxDurationMs int64 `validate:"gte=0"`
	MediaAllowedTypes  []string
	ChunkMaxBytes      int64 `validate:"gt=0"`
	S3Endpoint         string
	S3Bucket           string
	S3AccessKey        string
	S3SecretKey        string
	S3Region           string
	WorkerSize         int `validate:"gt=0"`
	MigrationsDir      string
	RunMigrations      bool
	SessionCookieName  string        `validate:"required"`
	SessionTTL         time.Duration `validate:"required,gt=0"`
	LogLevel           string        `validate:"required"`
}

var validate = validator.New()

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
	maxBytes, err := int64Or("MEDIA_MAX_BYTES", 0)
	if err != nil {
		return Config{}, err
	}
	maxDuration, err := int64Or("MEDIA_MAX_DURATION_MS", 0)
	if err != nil {
		return Config{}, err
	}
	chunkBytes, err := int64Or("CHUNK_MAX_BYTES", defaultChunkMaxBytes)
	if err != nil {
		return Config{}, err
	}
	workerSize, err := intOr("WORKER_SIZE", defaultWorkerSize)
	if err != nil {
		return Config{}, err
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
		OperatorID:         getenv("OPERATOR_ID"),
		MediaDriver:        getenv("MEDIA_DRIVER"),
		MediaPublicBaseURL: getenv("MEDIA_PUBLIC_BASE_URL"),
		MediaRoot:          getenv("MEDIA_ROOT"),
		MediaMaxBytes:      maxBytes,
		MediaMaxDurationMs: maxDuration,
		MediaAllowedTypes:  listOr("MEDIA_ALLOWED_TYPES"),
		ChunkMaxBytes:      chunkBytes,
		S3Endpoint:         getenv("S3_ENDPOINT"),
		S3Bucket:           getenv("S3_BUCKET"),
		S3AccessKey:        getenv("S3_ACCESS_KEY"),
		S3SecretKey:        getenv("S3_SECRET_KEY"),
		S3Region:           getenv("S3_REGION"),
		WorkerSize:         workerSize,
		MigrationsDir:      getenv("MIGRATIONS_DIR"),
		RunMigrations:      boolOr("RUN_MIGRATIONS"),
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
	if cfg.MediaDriver == DriverDisk && cfg.MediaRoot == "" {
		return Config{}, errors.New("MEDIA_ROOT is required when MEDIA_DRIVER=disk")
	}
	if cfg.RunMigrations && cfg.MigrationsDir == "" {
		return Config{}, errors.New("MIGRATIONS_DIR is required when RUN_MIGRATIONS is true")
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
