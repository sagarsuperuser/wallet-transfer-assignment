package db_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sagarsuperuser/wallet-transfer-assignment/internal/db"
	"github.com/sagarsuperuser/wallet-transfer-assignment/migrations"
)

// testPool connects to the database named by TEST_DATABASE_URL and applies the
// migrations. Tests skip when the variable is unset so that `go test ./...`
// works on a machine with no database; CI sets it.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return pool
}

func TestMigrateIsRepeatable(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()

	// testPool already migrated once; a second and third call must be no-ops.
	for i := 0; i < 2; i++ {
		if err := db.Migrate(ctx, p, migrations.FS); err != nil {
			t.Fatalf("re-running migrations: %v", err)
		}
	}

	var applied int
	if err := p.QueryRow(ctx,
		`SELECT count(*) FROM schema_migrations WHERE version = '0001_init.sql'`,
	).Scan(&applied); err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}

	if applied != 1 {
		t.Fatalf("migration recorded %d times, want exactly 1", applied)
	}
}

func TestMigrateCreatesEveryTable(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()

	for _, table := range []string{"wallets", "transfers", "ledger_entries"} {
		var exists bool
		if err := p.QueryRow(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, table,
		).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s was not created", table)
		}
	}
}

// TestLockTimeoutBoundsContention is the behavioural guarantee behind
// pessimistic locking: a transfer blocked by another transaction's row lock
// fails in bounded time instead of hanging until the client gives up.
func TestLockTimeoutBoundsContention(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()

	if _, err := p.Exec(ctx,
		`INSERT INTO wallets (id, balance) VALUES ('lock_timeout_probe', 100)
		 ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(),
			`DELETE FROM wallets WHERE id = 'lock_timeout_probe'`)
	})

	holder, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()

	if _, err := holder.Exec(ctx,
		`SELECT 1 FROM wallets WHERE id = 'lock_timeout_probe' FOR UPDATE`); err != nil {
		t.Fatalf("hold row lock: %v", err)
	}

	start := time.Now()
	_, err = p.Exec(ctx,
		`SELECT 1 FROM wallets WHERE id = 'lock_timeout_probe' FOR UPDATE`)
	waited := time.Since(start)

	if err == nil {
		t.Fatal("contending lock acquired the row while it was held")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("want lock_not_available (55P03), got %v", err)
	}

	if waited > 10*time.Second {
		t.Errorf("waited %v for a lock that should time out in about 3s", waited)
	}
}
