// Package dbtest provides the fixtures shared by the integration tests: a
// migrated database, and wallets to move money between.
//
// Integration tests run against a real PostgreSQL rather than a mock. A mocked
// query passes happily while the SQL underneath it is wrong, and the SQL is
// where the locking, the constraints, and the idempotency claim actually live.
package dbtest

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sagarsuperuser/wallet-transfer-assignment/internal/db"
	"github.com/sagarsuperuser/wallet-transfer-assignment/migrations"
)

var migrateOnce sync.Once

// Pool connects to the database named by TEST_DATABASE_URL and ensures the
// schema is applied. Tests skip when the variable is unset, so `go test ./...`
// works on a machine with no database; CI starts one and sets it.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping integration test")
	}

	pool, err := db.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)

	var migrateErr error
	migrateOnce.Do(func() {
		migrateErr = db.Migrate(context.Background(), pool, migrations.FS)
	})
	if migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}

	return pool
}

// Wallet inserts a wallet holding balance and returns its id.
//
// The id is unique per call so tests never contend for the same rows, which
// keeps them independent and lets them run in parallel.
func Wallet(t *testing.T, pool *pgxpool.Pool, balance int64) string {
	t.Helper()

	id := "wallet_" + uuid.NewString()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO wallets (id, balance) VALUES ($1, $2)`, id, balance); err != nil {
		t.Fatalf("seed wallet: %v", err)
	}

	return id
}

// Balance reads a wallet's stored balance with a plain query, independent of
// the code under test, so an assertion cannot be fooled by the same bug twice.
func Balance(t *testing.T, pool *pgxpool.Pool, walletID string) int64 {
	t.Helper()

	var balance int64
	if err := pool.QueryRow(context.Background(),
		`SELECT balance FROM wallets WHERE id = $1`, walletID).Scan(&balance); err != nil {
		t.Fatalf("read balance of %s: %v", walletID, err)
	}

	return balance
}
