package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ysunote/grafana-light/internal/config"
	"github.com/ysunote/grafana-light/internal/monitor"
	"github.com/ysunote/grafana-light/internal/server"
	"github.com/ysunote/grafana-light/internal/tui"
)

func main() {
	configPath := flag.String("config", "configs/example.yaml", "設定ファイルのパス")
	mode := flag.String("mode", "server", "実行モード: server, tui, once")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("設定読み込みエラー: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mon := monitor.New(cfg.Prometheus.URL, cfg.Prometheus.Job, cfg.Devices, cfg.Server.Interval.Unwrap())

	switch *mode {
	case "server":
		if err := runServer(ctx, cfg, mon); err != nil {
			log.Fatalf("server モードエラー: %v", err)
		}
	case "tui":
		go mon.Run(ctx)
		if err := tui.RunInteractive(ctx, mon, cfg, *configPath); err != nil {
			log.Fatalf("tui モードエラー: %v", err)
		}
	case "once":
		snapCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := mon.Refresh(snapCtx); err != nil {
			log.Fatalf("once モードエラー: %v", err)
		}
		color := isTerminal(os.Stdout)
		fmt.Fprint(os.Stdout, tui.RenderSnapshot(mon.Devices(), time.Now(), color))
	default:
		log.Fatalf("不明な mode: %s (server, tui, once のいずれかを指定)", *mode)
	}
}

func runServer(ctx context.Context, cfg *config.Config, mon *monitor.Monitor) error {
	go mon.Run(ctx)

	srv := server.New(mon)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSEのため無制限
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("grafana-light 起動: http://localhost%s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("サーバーエラー: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("シャットダウン中...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		return err
	}
	log.Println("正常終了")
	return nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
