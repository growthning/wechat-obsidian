package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/user/wechat-obsidian/internal/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("wechat-obsidian server starting on port %d\n", cfg.Server.Port)
	fmt.Printf("  data dir: %s\n", cfg.Storage.DataDir)
	fmt.Printf("  article timeout: %s, max images: %d\n", cfg.Article.Timeout, cfg.Article.MaxImages)
}
