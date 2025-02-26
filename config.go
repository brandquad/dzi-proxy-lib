package dziproxylib

import (
	"time"
)

var LibConfig *Config

type Config struct {
	Listen            string
	S3AccessKey       string
	S3SecretKey       string
	S3Region          string
	S3Bucket          string
	S3Host            string
	S3UseSSL          bool
	CleanupTimeoutCfg int
	CleanupTimeout    time.Duration
	CacheDir          string
	HttpCacheDays     int
}
