// Package db wires the SingleStoreDB connection pool used by all
// repositories. SingleStore speaks the MySQL wire protocol, so we use
// go-sql-driver/mysql with TLS enabled.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/tiagocastro/singlescore/backend/internal/config"
)

// Open returns a *sql.DB pool pointing at the configured SingleStore
// cluster. The pool is lazy; call Ping to verify reachability.
func Open(cfg config.DBConfig) (*sql.DB, error) {
	// DSN:  user:pass@tcp(host:port)/db?params
	params := url.Values{}
	params.Set("parseTime", "true")
	params.Set("loc", "UTC")
	params.Set("tls", "true")
	params.Set("multiStatements", "false")
	params.Set("interpolateParams", "true")
	params.Set("charset", "utf8mb4")

	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database, params.Encode(),
	)

	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}

	// Conservative pool defaults for a starter-sized cluster.
	pool.SetMaxOpenConns(16)
	pool.SetMaxIdleConns(8)
	pool.SetConnMaxLifetime(30 * time.Minute)
	pool.SetConnMaxIdleTime(5 * time.Minute)

	return pool, nil
}

// Ping checks SingleStore reachability with a short deadline.
func Ping(ctx context.Context, pool *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return pool.PingContext(ctx)
}
