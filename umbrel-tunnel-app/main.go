package main

import (
	"embed"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/albit/umbreltunnel-app/internal/api"
	"github.com/albit/umbreltunnel-app/internal/config"
	"github.com/albit/umbreltunnel-app/internal/wireguard"
)

//go:embed web/*
var webFS embed.FS

func main() {
	cfg := config.Load()

	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		log.Fatalf("creating data dir: %v", err)
	}

	wgMgr := wireguard.NewManager(cfg.DataDir, cfg.WGInterface)

	srv := api.NewServer(cfg, wgMgr)
	srv.SetWebFS(webFS, "web")

	if cfgContent := wgMgr.GetConfigContent(); cfgContent != "" {
		if !wgMgr.IsUp() {
			if err := wgMgr.ApplyConfig(cfgContent); err != nil {
				log.Printf("auto-reconnect wireguard: %v", err)
			}
		}
		if wgMgr.IsUp() {
			srv.RestoreForwarders()
		}
	}

	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("Umbrel Tunnel listening on %s", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	server.Close()
	log.Println("Done.")
}
