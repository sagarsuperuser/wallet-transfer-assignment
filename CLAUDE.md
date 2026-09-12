# Wallet Transfer Service — working context

## What this is

A take-home assignment. The brief is [ASSIGNMENT.md](./ASSIGNMENT.md); the
reviewer's rubric is [evaluation_guide.md](./evaluation_guide.md). Read both
before proposing anything.

Scoped at **3–5 hours**. Every design decision has to be defensible in a review
discussion afterwards, so reasoning matters more than volume of code.

## Working agreement

- **I decide, you implement.** The design decisions below are made. Challenge
  one if you think it's actually wrong — say so directly — but don't relitigate
  them by default.
- **Simple beats clever.** The rubric asks explicitly whether the solution is
  simpler than it needs to be, and the review instructions tell the reviewer to
  flag over-engineering. Don't add layers, interfaces, abstractions, config, or
  generality I didn't ask for.
- **Plain SQL. No ORM.**
- **Ask before adding any dependency.**
- **Keep the reasoning visible.** This session's transcript is submitted with
  the assignment.

## Stack

Go, PostgreSQL. Money is `BIGINT` in minor units — never a float, anywhere.

## Layering

    handler     HTTP only: parse, validate shape, map errors to status codes
    service     business logic, orchestration, transaction boundaries
    repository  persistence only — no workflow decisions
    domain      entities, state transitions, validation rules

Handlers stay thin. Repositories don't decide anything.

## Design decisions (made — see PR description for the full reasoning)

**1. Stored balance, not derived.** `wallets.balance` with
`CHECK (balance >= 0)`, so an overdraft is impossible to *store* rather than
merely prevented by application logic. The ledger remains the audit record, and
a test asserts stored balance equals the sum of ledger entries.

**2. Pessimistic locking.** `SELECT ... FOR UPDATE` on both wallet rows.
Reading and writing inside the same lock means the balance checked is the
balance written against — no read-then-write race, no retry loop. Optimistic
versioning degrades worst under exactly the contention a wallet system produces.

**3. Idempotency by unique constraint.** `UNIQUE` on
`transfers.idempotency_key`, claimed with
`INSERT ... ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`. No row back
means the key is already owned, so read and return the existing transfer — the
database arbitrates, not a check-then-insert that could race. A `request_hash`
column catches the same key sent with different parameters: that returns `409`
rather than silently returning an unrelated transfer. No separate idempotency
table, because the response is fully derivable from the transfer row.

**4. State machine.** A transfer is created `PENDING` and transitions to
`PROCESSED` or `FAILED` inside one transaction, so `PENDING` is never externally
observable. Insufficient funds is recorded as `FAILED` **and committed**, not
rolled back — idempotency requires the same key to produce the same answer, so a
retry must see the original failure. Transitions are guarded:
`WHERE id = $1 AND state = 'PENDING'`.

**5. Lock ordering.** Wallet rows are locked `ORDER BY id`, so two transfers in
opposite directions between the same pair can't form a lock cycle. Self-transfer
is rejected by a `CHECK` constraint and in the domain layer.

## Transaction shape

One transaction for the whole transfer:

1. Claim the idempotency key (`ON CONFLICT DO NOTHING RETURNING id`).
   No row → read the existing transfer and return it, without taking wallet locks.
2. `SELECT ... FROM wallets WHERE id IN ($from,$to) ORDER BY id FOR UPDATE`
3. Insufficient funds → mark `FAILED`, commit, return 422.
4. Update both balances with `balance = balance ± $amount` (relative, never
   absolute — the arithmetic belongs in the database where it's serialised).
5. Insert both ledger entries.
6. Mark `PROCESSED`. Commit.

## Schema

```sql
CREATE TABLE wallets (
    id         TEXT PRIMARY KEY,
    balance    BIGINT NOT NULL CHECK (balance >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE transfers (
    id              TEXT PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_hash    TEXT NOT NULL,
    from_wallet_id  TEXT NOT NULL REFERENCES wallets(id),
    to_wallet_id    TEXT NOT NULL REFERENCES wallets(id),
    amount          BIGINT NOT NULL CHECK (amount > 0),
    state           TEXT NOT NULL CHECK (state IN ('PENDING','PROCESSED','FAILED')),
    failure_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (from_wallet_id <> to_wallet_id)
);

CREATE TABLE ledger_entries (
    id          BIGSERIAL PRIMARY KEY,
    transfer_id TEXT NOT NULL REFERENCES transfers(id),
    wallet_id   TEXT NOT NULL REFERENCES wallets(id),
    type        TEXT NOT NULL CHECK (type IN ('DEBIT','CREDIT')),
    amount      BIGINT NOT NULL CHECK (amount > 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (transfer_id, wallet_id, type)
);

CREATE INDEX idx_ledger_wallet ON ledger_entries(wallet_id, id);
```

## Tests required

- happy path: balances move, exactly two ledger entries, transfer `PROCESSED`
- duplicate idempotency key returns the original transfer, no second transfer
- same key with different parameters returns `409`
- insufficient funds records `FAILED` and moves no money
- two concurrent debits on the same wallet don't double-spend
- every wallet's stored balance equals the sum of its ledger entries

Behavioural assertions, not implementation details. Integration tests run
against real Postgres — the database is never mocked, because a mocked query
passes happily while the SQL is wrong.

## Commits

Topical commits with real messages. The rubric grades commit hygiene explicitly.
