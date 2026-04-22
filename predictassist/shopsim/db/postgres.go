package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// schema contains the DDL statements executed on startup.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id          SERIAL PRIMARY KEY,
		email       TEXT UNIQUE NOT NULL,
		name        TEXT NOT NULL,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS products (
		id          SERIAL PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT,
		price       NUMERIC(10,2) NOT NULL,
		stock       INT NOT NULL DEFAULT 0,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS orders (
		id          SERIAL PRIMARY KEY,
		user_id     INT NOT NULL REFERENCES users(id),
		product_id  INT NOT NULL REFERENCES products(id),
		quantity    INT NOT NULL,
		total       NUMERIC(10,2) NOT NULL,
		status      TEXT NOT NULL DEFAULT 'pending',
		created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS payments (
		id          SERIAL PRIMARY KEY,
		order_id    INT NOT NULL REFERENCES orders(id),
		amount      NUMERIC(10,2) NOT NULL,
		status      TEXT NOT NULL DEFAULT 'pending',
		created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
}

// Connect creates a connection pool to the given PostgreSQL DSN and runs
// schema migrations.
func Connect(ctx context.Context, dsn string, logger *zap.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing dsn: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	if err := migrate(ctx, pool, logger); err != nil {
		pool.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	logger.Info("connected to PostgreSQL", zap.String("dsn", dsn))
	return pool, nil
}

func migrate(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	for _, ddl := range schema {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("executing migration: %w", err)
		}
	}
	logger.Info("database schema migrated")
	return nil
}
