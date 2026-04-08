package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type CleanupConfig struct {
	Enabled       bool          `yaml:"enabled"`
	RetentionDays int           `yaml:"retention_days"`
	Interval      time.Duration `yaml:"interval"`
}

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	WeChat  WeChatConfig  `yaml:"wechat"`
	Storage StorageConfig `yaml:"storage"`
	Article ArticleConfig `yaml:"article"`
	Cleanup CleanupConfig `yaml:"cleanup"`
}

type ServerConfig struct {
	Port   int    `yaml:"port"`
	APIKey string `yaml:"api_key"`
}

type WeChatConfig struct {
	CorpID         string `yaml:"corp_id"`
	AgentID        int    `yaml:"agent_id"`
	Secret         string `yaml:"secret"`
	Token          string `yaml:"token"`
	EncodingAESKey string `yaml:"encoding_aes_key"`
	KFCorpID         string `yaml:"kf_corp_id"`          // 微信客服平台的企业 ID
	KFSecret         string `yaml:"kf_secret"`           // 微信客服平台的 Secret
	KFToken          string `yaml:"kf_token"`            // 微信客服回调 Token
	KFEncodingAESKey string `yaml:"kf_encoding_aes_key"` // 微信客服回调 EncodingAESKey
}

type StorageConfig struct {
	DataDir string `yaml:"data_dir"`
}

type ArticleConfig struct {
	Timeout   time.Duration `yaml:"timeout"`
	MaxImages int           `yaml:"max_images"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Port: 8900,
		},
		Storage: StorageConfig{
			DataDir: "./data",
		},
		Article: ArticleConfig{
			Timeout:   30 * time.Second,
			MaxImages: 50,
		},
		Cleanup: CleanupConfig{
			Enabled:       true,
			RetentionDays: 7,
			Interval:      1 * time.Hour,
		},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return cfg, nil
}
