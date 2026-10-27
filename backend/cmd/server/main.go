// Package main is the SingleScore backend entry point.
//
// It wires config, logging, the SingleStore pool, and the gqlgen GraphQL
// handler onto a plain net/http server (no third-party routers). The
// server supports graceful shutdown on SIGINT / SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/tiagocastro/singlescore/backend/internal/config"
	"github.com/tiagocastro/singlescore/backend/internal/db"
	"github.com/tiagocastro/singlescore/backend/internal/graph"
	"github.com/tiagocastro/singlescore/backend/internal/logging"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
	"github.com/tiagocastro/singlescore/backend/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Logger isn't up yet; write to stderr and exit.
		_, _ = os.Stderr.WriteString("config: " + err.Error() + "\n")
		os.Exit(2)
	}
	logger := logging.New(cfg.LogLevel)
	logger.Info("starting singlescore backend", "version", cfg.BuildVersion, "addr", cfg.HTTPAddr)

	pool, err := db.Open(cfg.DB)
	if err != nil {
		logger.Error("db open failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	startupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.Ping(startupCtx, pool); err != nil {
		// Health re-pings per request, so the API recovers on its own.
		logger.Warn("db ping failed; continuing in degraded mode", "err", err)
	} else {
		logger.Info("db connected", "host", cfg.DB.Host, "db", cfg.DB.Database)
	}

	catalogRepo := repository.NewCatalog(pool)
	matchesRepo := repository.NewMatches(pool)
	predsRepo := repository.NewPredictions(pool)
	ingestRepo := repository.NewIngestion(pool)
	svc := service.New(catalogRepo, matchesRepo, predsRepo, ingestRepo)

	resolver := graph.NewResolver(graph.Deps{
		Pool:      pool,
		Service:   svc,
		Version:   cfg.BuildVersion,
		StartedAt: time.Now().UTC(),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/graphql", newGraphQLHandler(graph.NewExecutableSchema(graph.Config{Resolvers: resolver}), logger))
	// The Playground is a local-debugging aid only.
	if strings.EqualFold(cfg.LogLevel, "debug") {
		mux.Handle("/", playground.Handler("SingleScore GraphQL", "/graphql"))
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           withCORS(cfg.CORSOrigins, withRequestLogger(logger, mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	exitCode := 0
	select {
	case sig := <-stop:
		logger.Info("shutdown requested", "signal", sig.String())
	case err := <-errCh:
		logger.Error("http server crashed", "err", err)
		exitCode = 1
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "err", err)
	}
	logger.Info("bye")
	if exitCode != 0 {
		pool.Close()
		os.Exit(exitCode)
	}
}

// newGraphQLHandler serves the schema over GET and POST only (no
// websockets or uploads: the API is read-only), caches parsed queries,
// and keeps resolver errors (often SQL) out of responses: they're logged
// and the client gets a generic message.
func newGraphQLHandler(schema graphql.ExecutableSchema, logger *slog.Logger) http.Handler {
	srv := handler.New(schema)
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	srv.Use(extension.Introspection{})
	srv.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		gqlErr := graphql.DefaultErrorPresenter(ctx, err)
		if gqlErr.Err != nil {
			logger.Error("graphql resolver error", "path", gqlErr.Path.String(), "err", err)
			gqlErr.Message = "internal error"
		}
		return gqlErr
	})
	return srv
}

// withCORS adds minimal CORS headers for the configured origins. We do
// this ourselves so we don't need a third-party router/middleware stack.
func withCORS(allowed []string, next http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[o] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if _, ok := set[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withRequestLogger(l interface {
	Info(msg string, args ...any)
}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		l.Info("http", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}
