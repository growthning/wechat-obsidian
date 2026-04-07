package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/gin-gonic/gin"

	"github.com/user/wechat-obsidian/internal/config"
	"github.com/user/wechat-obsidian/internal/fetcher"
	"github.com/user/wechat-obsidian/internal/handler"
	"github.com/user/wechat-obsidian/internal/store"
	"github.com/user/wechat-obsidian/internal/wechat"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
		os.Exit(1)
	}

	// init store
	db, err := store.New(cfg.Storage.DataDir)
	if err != nil {
		log.Fatalf("failed to init store: %v", err)
	}
	defer db.Close()

	// init fetcher
	imageDir := filepath.Join(cfg.Storage.DataDir, "images")
	f := fetcher.New(imageDir, cfg.Article.Timeout, cfg.Article.MaxImages)

	// init KF client
	kfCorpID := cfg.WeChat.KFCorpID
	if kfCorpID == "" {
		kfCorpID = cfg.WeChat.CorpID
	}
	kfSecret := cfg.WeChat.KFSecret
	if kfSecret == "" {
		kfSecret = cfg.WeChat.Secret
	}
	kfClient := wechat.NewKFClient(kfCorpID, kfSecret)

	// init handlers
	wechatHandler := handler.NewWeChatHandler(&cfg.WeChat, db, f, kfClient)
	syncHandler := handler.NewSyncHandler(cfg.Server.APIKey, db, f)
	imagesHandler := handler.NewImagesHandler(cfg.Server.APIKey, db)

	// setup routes
	r := gin.Default()

	r.GET("/api/wechat/callback", wechatHandler.VerifyURL)
	r.POST("/api/wechat/callback", wechatHandler.HandleCallback)
	r.GET("/api/sync", syncHandler.GetMessages)
	r.POST("/api/sync/ack", syncHandler.AckMessages)
	r.GET("/api/images/:filename", imagesHandler.ServeImage)
	r.POST("/api/save", syncHandler.SaveURL)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("shutting down...")
		db.Close()
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("wechat-obsidian server starting on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
