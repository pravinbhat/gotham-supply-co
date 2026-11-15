// batcave-order-ops: one process = REST API + Temporal worker.
//
// Temporal connection settings come from envconfig: TEMPORAL_ADDRESS,
// TEMPORAL_NAMESPACE, TEMPORAL_API_KEY (or TEMPORAL_PROFILE + temporal.toml).
// With nothing set it falls back to localhost:7233 / "default".
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/activities"
	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/api"
	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/shared"
	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/store"
	"github.com/pravinbhat/gotham-supply-co/services/batcave-order-ops/internal/workflows"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	dbPath := env("DB_PATH", "data/order-ops.db")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)
	st, err := store.Open(dbPath)
	if err != nil {
		logger.Error("open db", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	opts, err := envconfig.LoadDefaultClientOptions()
	if err != nil {
		logger.Error("load temporal client config", "error", err)
		os.Exit(1)
	}
	opts.Logger = tlog.NewStructuredLogger(logger)
	c, err := client.Dial(opts)
	if err != nil {
		logger.Error("dial temporal", "error", err)
		os.Exit(1)
	}
	defer c.Close()
	logger.Info("connected to Temporal", "hostPort", opts.HostPort, "namespace", opts.Namespace)

	w := worker.New(c, shared.TaskQueue, worker.Options{})
	w.RegisterWorkflow(workflows.OrderWorkflow)
	w.RegisterActivity(&activities.Activities{
		HTTP:       &http.Client{Timeout: 25 * time.Second}, // < activity StartToClose (30s)
		Store:      st,
		BillingURL: env("BILLING_URL", "http://localhost:8082"),
		ArmoryURL:  env("ARMORY_URL", "http://localhost:8083"),
	})
	if err := w.Start(); err != nil {
		logger.Error("start worker", "error", err)
		os.Exit(1)
	}
	defer w.Stop()

	srv := &http.Server{
		Addr:              ":" + env("PORT", "8081"),
		Handler:           (&api.Server{Temporal: c, Store: st}).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("batcave-order-ops listening", "addr", srv.Addr, "taskQueue", shared.TaskQueue)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
