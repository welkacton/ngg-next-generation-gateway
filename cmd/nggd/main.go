// nggd is the NGG Phase 1 daemon: unified core management center.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ngg-project/ngg/internal/api"
	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/engine"
)

func main() {
	var (
		configPath = flag.String("config", "configs/ngg.yaml", "NGG config file")
		noRestore  = flag.Bool("no-restore", false, "do not restore previously running kernels")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Printf("NGG %s\n", engine.NGGVersion)
		return
	}
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.Printf("NGG %s starting", engine.NGGVersion)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		log.Fatalf("init engine: %v", err)
	}
	srv := api.New(cfg, eng)

	if !*noRestore {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		results := eng.Restore(ctx)
		cancel()
		for _, r := range results {
			if !r.OK {
				log.Printf("restore %s failed (other kernels unaffected): %s", r.ID, r.Message)
			}
		}
	}

	// Stage 2: start the NGG-owned network engine when configured. A failure
	// here is logged but never fatal — kernels and the control plane stay up.
	if cfg.Network.Enabled {
		nctx, ncancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := eng.NetworkStart(nctx); err != nil {
			log.Printf("NGG network engine did not start: %v (kernels and control plane unaffected)", err)
		} else {
			log.Printf("NGG unified network engine started (ingress %s:%d)", cfg.Network.Ingress.Listen, cfg.Network.Ingress.Port)
		}
		ncancel()
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Printf("NGG control plane listening on http://%s (local-only, token required)", srv.Addr())
	log.Printf("Registered kernels: %d | Stage 1 | mode: multi-core-parallel", len(eng.Registry()))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if err != nil {
			log.Printf("http server stopped: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("received %s, shutting down kernels", sig)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = srv.Shutdown()
	if err := eng.Shutdown(ctx); err != nil {
		log.Printf("engine shutdown: %v", err)
	}
	log.Printf("NGG stopped")
}
