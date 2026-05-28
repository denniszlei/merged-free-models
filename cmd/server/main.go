// Command server is the merged-free-models entrypoint.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denniszlei/merged-free-models/internal/config"
	"github.com/denniszlei/merged-free-models/internal/httpapi"
	"github.com/denniszlei/merged-free-models/internal/provider"
	"github.com/denniszlei/merged-free-models/internal/provider/kilo"
	"github.com/denniszlei/merged-free-models/internal/provider/opencode"
	"github.com/denniszlei/merged-free-models/internal/version"
)

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	client := &http.Client{}
	var providers []provider.Provider
	if cfg.Kilo.Enabled {
		providers = append(providers, kilo.New(client, cfg.Kilo, cfg.ModelFetchTimeout))
	}
	if cfg.OpenCode.Enabled {
		providers = append(providers, opencode.New(client, cfg.OpenCode, cfg.ModelFetchTimeout))
	}

	registry := provider.NewRegistry(cfg.RefreshInterval, providers...)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("merged-free-models %s (%s) starting; %d provider(s) enabled",
		version.Version, version.Commit, len(providers))

	registry.RefreshAll(ctx) // best effort; errors are already logged inside
	go registry.Run(ctx)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.NewServer(registry, cfg.Addr, cfg.ProxyAPIKey),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
