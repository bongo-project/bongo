package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// store is what the handler and the rollup job need from the database.
type store interface {
	// Record notes that an instance was seen on day with version. Recording
	// the same triple twice is not an error.
	Record(ctx context.Context, day, version, id string) error
	// Rollup counts every day before today into daily_counts, deletes those
	// days' IDs and deletes counts for days before cutoff, in one transaction.
	Rollup(ctx context.Context, today, cutoff string) error
}

// pgStore is the PostgreSQL implementation.
type pgStore struct {
	pool *pgxpool.Pool
}

// errDatabaseConfig is returned for a DSN that does not parse. Its text is
// fixed: the parser's own message can carry parts of the DSN.
var errDatabaseConfig = errors.New("database configuration is invalid")

// openStore builds the pool (connections are made lazily) from dsn.
func openStore(dsn string) (*pgStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errDatabaseConfig
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, errDatabaseConfig
	}
	return &pgStore{pool: pool}, nil
}

// Close releases the pool.
func (s *pgStore) Close() { s.pool.Close() }

// EnsureSchema creates the tables when they are missing.
func (s *pgStore) EnsureSchema(ctx context.Context) error {
	for _, q := range []string{createSeen, createDailyCounts} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}
	return nil
}

// Record implements store.
func (s *pgStore) Record(ctx context.Context, day, version, id string) error {
	if _, err := s.pool.Exec(ctx, insertSeen, day, version, id); err != nil {
		return fmt.Errorf("record seen: %w", err)
	}
	return nil
}

// Rollup implements store.
func (s *pgStore) Rollup(ctx context.Context, today, cutoff string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, rollupSQL, today); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, retentionSQL, cutoff)
		return err
	})
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	return nil
}

// describeErr is the only text about a database error that reaches a log:
// the SQLSTATE when there is one, otherwise a fixed phrase. Driver messages
// can name the host, user and database, so they are never logged.
func describeErr(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return "sqlstate " + pe.Code
	}
	return "database error"
}

// errNotReachable is the fixed startup failure; it carries no driver text.
var errNotReachable = errors.New("database is not reachable")

// waitForSchema retries EnsureSchema until it works or total elapses.
func waitForSchema(ctx context.Context, s *pgStore, every, total time.Duration) error {
	deadline := time.Now().Add(total)
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.EnsureSchema(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil || time.Now().Add(every).After(deadline) {
			return errNotReachable
		}
		select {
		case <-ctx.Done():
			return errNotReachable
		case <-time.After(every):
		}
	}
}
