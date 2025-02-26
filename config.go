package dziproxy

import (
	"github.com/kelseyhightower/envconfig"
	"log"
	"os"
	"time"
)

var Config *config

type config struct {
	Listen            string `envconfig:"LISTEN" required:"true" default:"0.0.0.0:1192"`
	S3AccessKey       string `envconfig:"S3_ACCESS_KEY" required:"true"`
	S3SecretKey       string `envconfig:"S3_SECRET_KEY" required:"true"`
	S3Region          string `envconfig:"S3_REGION" required:"true" default:"us-east-1"`
	S3Bucket          string `envconfig:"S3_BUCKET" required:"true"`
	S3Host            string `envconfig:"S3_HOST" required:"true"`
	S3UseSSL          bool   `envconfig:"S3_USE_SSL" default:"true"`
	CleanupTimeoutCfg int    `envconfig:"CLEANUP_TIMEOUT" default:"20"`
	CleanupTimeout    time.Duration
	CacheDir          string `envconfig:"CACHE_DIR" default:"./.cache"`
	HttpCacheDays     int    `envconfig:"HTTP_CACHE_DAYS" default:"20"`
}

func init() {
	var c config
	if err := envconfig.Process("", &c); err != nil {
		log.Fatalln(err)
	}
	c.CleanupTimeout = time.Duration(c.CleanupTimeoutCfg) * time.Minute
	if _, err := os.Stat(c.CacheDir); os.IsNotExist(err) {
		if err = os.MkdirAll(c.CacheDir, os.ModePerm); err != nil {
			log.Fatalln(err)
		}
	}
	Config = &c
}
