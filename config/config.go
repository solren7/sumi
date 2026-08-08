package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Insecure placeholder values that must be overridden in production.
var insecureDefaults = map[string]string{
	"JWT_SECRET":           "secret",
	"REFRESH_TOKEN_PEPPER": "refresh-pepper",
	"API_KEY_PEPPER":       "api-key-pepper",
}

type Config struct {
	DBDSN                  string `env:"DB_DSN"`
	RedisConfig            RedisConfig
	ServerPort             string        `env:"SERVER_PORT" envDefault:":3000"`
	LogFormat              string        `env:"LOG_FORMAT" envDefault:"json"`
	JWTSecret              string        `env:"JWT_SECRET" envDefault:"secret"`
	AccessTokenTTL         time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL        time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"`
	RefreshTokenPepper     string        `env:"REFRESH_TOKEN_PEPPER" envDefault:"refresh-pepper"`
	APIKeyPepper           string        `env:"API_KEY_PEPPER" envDefault:"api-key-pepper"`
	DefaultCurrency        string        `env:"DEFAULT_CURRENCY" envDefault:"CNY"`
	DefaultTimezone        string        `env:"DEFAULT_TIMEZONE" envDefault:"Asia/Shanghai"`
	StatsCacheTTL          time.Duration `env:"STATS_CACHE_TTL" envDefault:"10m"`
	CategoryCacheTTL       time.Duration `env:"CATEGORY_CACHE_TTL" envDefault:"12h"`
	APIKeyCacheTTL         time.Duration `env:"API_KEY_CACHE_TTL" envDefault:"15m"`
	RefreshTokenCacheExtra time.Duration `env:"REFRESH_TOKEN_CACHE_EXTRA_TTL" envDefault:"0s"`
	AutoMigrate            bool          `env:"AUTO_MIGRATE" envDefault:"false"`
	MigrationsDir          string        `env:"MIGRATIONS_DIR" envDefault:"./migrations"`
	MigrationTimeout       time.Duration `env:"MIGRATION_TIMEOUT" envDefault:"5m"`
	AllowedOrigins         string        `env:"ALLOWED_ORIGINS" envDefault:"*"`
	Production             bool          `env:"PRODUCTION" envDefault:"false"`
}

type RedisConfig struct {
	// 基础连接信息
	RedisURL string `env:"REDIS_URL" envDefault:"redis://localhost:6379/0"`

	// 超时设置 (配置库会自动解析 "10s", "500ms" 等字符串为 time.Duration)
	DialTimeout  time.Duration `env:"REDIS_DIAL_TIMEOUT" envDefault:"5s"`
	ReadTimeout  time.Duration `env:"REDIS_READ_TIMEOUT" envDefault:"3s"`
	WriteTimeout time.Duration `env:"REDIS_WRITE_TIMEOUT" envDefault:"3s"`

	// 连接池设置
	PoolSize      int  `env:"REDIS_POOL_SIZE" envDefault:"10"`
	MinIdleConns  int  `env:"REDIS_MIN_IDLE_CONNS" envDefault:"5"`
	IsClusterMode bool `env:"REDIS_IS_CLUSTER_MODE" envDefault:"false"`
}

func NewConfig() *Config {
	_ = godotenv.Load()
	var cfg Config

	if err := env.Parse(&cfg); err != nil {
		panic(err)
	}

	if cfg.Production {
		if cfg.JWTSecret == insecureDefaults["JWT_SECRET"] {
			panic(fmt.Sprintf("FATAL: JWT_SECRET is set to the insecure default value %q — set a strong secret via the JWT_SECRET env var", cfg.JWTSecret))
		}
		if cfg.RefreshTokenPepper == insecureDefaults["REFRESH_TOKEN_PEPPER"] {
			panic(fmt.Sprintf("FATAL: REFRESH_TOKEN_PEPPER is set to the insecure default value %q — set a strong value via the REFRESH_TOKEN_PEPPER env var", cfg.RefreshTokenPepper))
		}
		if cfg.APIKeyPepper == insecureDefaults["API_KEY_PEPPER"] {
			panic(fmt.Sprintf("FATAL: API_KEY_PEPPER is set to the insecure default value %q — set a strong value via the API_KEY_PEPPER env var", cfg.APIKeyPepper))
		}
		if len(cfg.JWTSecret) < 32 {
			panic("FATAL: JWT_SECRET must be at least 32 characters in production")
		}
	}

	return &cfg
}
