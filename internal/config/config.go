package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	WeChat  WeChatConfig  `yaml:"wechat"`
	Storage StorageConfig `yaml:"storage"`
	Article ArticleConfig `yaml:"article"`
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
	KFSecret       string `yaml:"kf_secret"` // 微信客服应用的 secret (may differ from self-built app secret)
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
