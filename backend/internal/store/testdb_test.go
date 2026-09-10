package store

// testdb_test.go — shared PostgreSQL harness for store tests.
//
// Every DB-backed test in this package (and every persistence unit that
// lands after it) obtains its connection through testDB(t). The harness is
// gated on the KUBECENTER_TEST_DATABASE_URL environment variable:
//
//   - unset or empty → the calling test is skipped with a message naming
//     the variable, so `go test ./...` stays green on a laptop with no
//     database;
//   - set → the harness connects, applies the embedded migrations through
//     the real runner (store.New → DB.migrate) exactly once per process,
//     and hands each test its own small pool.
//
// There is deliberately NO //go:build tag on this file. The repo's canonical
// check is repo-wide `go test ./...` (CLAUDE.md Agent Directive 4); a build
// tag would exclude these tests from that command and they would never run.
// Env-gating keeps them in the default graph while letting CI — which runs a
// postgres:17-alpine service and sets the variable — actually execute them.
//
// Production-safety guard: the harness reads ONLY KUBECENTER_TEST_DATABASE_URL.
// It never falls back to KUBECENTER_DATABASE_URL (the runtime variable), so a
// developer shell that has the real deployment URL exported cannot accidentally
// run migrations or test writes against it. Point the test variable at a
// throwaway database you are happy to have mutated.
//
// Isolation contract: the harness never drops the schema and never truncates
// shared tables — later units may run their suites in parallel against the
// same database, and teardown-based isolation would race. Instead, every
// suite MUST scope the rows it writes and reads by a unique-per-test
// identifier from testOwnerID(t) (or an equivalently unique key for tables
// that are not owner-scoped). Rows left behind by a failed run are harmless
// because no other test will ever look them up by the same identifier.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDatabaseURLEnv is the only environment variable the harness consults.
const testDatabaseURLEnv = "KUBECENTER_TEST_DATABASE_URL"

// testDBConnectTimeout bounds the initial connect + migrate pass. store.New
// retries with backoff for up to ~90s on a dead host; this shorter ceiling
// turns a typo in the URL into a fast failure instead of a long hang.
const testDBConnectTimeout = 30 * time.Second

var (
	// migrateOnce guards the once-per-process migration pass. The pool
	// opened for migrating is closed immediately afterwards; tests get
	// their own pools so a test that poisons a connection (cancelled
	// context mid-transaction, leaked Acquire) cannot affect siblings.
	migrateOnce sync.Once
	migrateErr  error
)

// testDatabaseURL returns the value of KUBECENTER_TEST_DATABASE_URL, or ""
// when it is unset or empty. It is the single decision point for gating
// and is kept pure (lookup injected) so the gating rule is unit-testable
// without a database.
func testDatabaseURL(lookup func(string) (string, bool)) string {
	v, ok := lookup(testDatabaseURLEnv)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// testDB returns a live pool against the migrated test database, or skips
// the calling test when KUBECENTER_TEST_DATABASE_URL is not set.
//
// Migrations are applied at most once per test binary. The returned pool is
// private to the calling test and is closed in t.Cleanup; the harness does
// not truncate or drop anything — see the isolation contract at the top of
// this file.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	connString := testDatabaseURL(os.LookupEnv)
	if connString == "" {
		t.Skipf("%s is not set; skipping PostgreSQL-backed test", testDatabaseURLEnv)
	}

	migrateOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testDBConnectTimeout)
		defer cancel()

		// store.New is the production entry point: it builds the pool,
		// pings it, and runs DB.migrate over the embedded migrations. Using
		// it (rather than a hand-rolled migrator) means the test schema is
		// produced by exactly the code path the binary uses at boot.
		db, err := New(ctx, connString, 0, 0, testLogger())
		if err != nil {
			migrateErr = fmt.Errorf("connecting to %s and applying migrations: %w", testDatabaseURLEnv, err)
			return
		}
		db.Close()
	})
	if migrateErr != nil {
		t.Fatalf("test database unavailable: %v", migrateErr)
	}

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		t.Fatalf("parsing %s: %v", testDatabaseURLEnv, err)
	}
	// Small per-test pool: connections are opened lazily on first Acquire,
	// so idle tests cost nothing, and a low ceiling keeps parallel suites
	// well under PostgreSQL's default max_connections.
	config.MaxConns = 4
	config.MinConns = 0

	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("creating test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// testOwnerID returns an identifier unique to the calling test invocation:
// a sanitized form of t.Name() followed by a random 8-byte hex suffix.
//
// The name prefix makes stray rows attributable when inspecting a shared
// database; the random suffix guarantees that parallel tests, `-count=N`
// reruns, and reruns after a failed cleanup never collide on the same key.
// Suites MUST use this (or something equally unique) as the owner_id / user
// scope for every row they write, and MUST filter by it on every read.
func testOwnerID(t *testing.T) string {
	t.Helper()

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generating owner id suffix: %v", err)
	}
	return ownerIDFor(t.Name(), hex.EncodeToString(suffix[:]))
}

// ownerIDPrefix is the fixed leading segment of every harness-generated id,
// so rows written by tests are recognisable at a glance in a shared database.
const ownerIDPrefix = "test"

// ownerIDMaxNameLen caps the sanitized test-name segment. Deeply nested
// subtests can produce very long names; the random suffix carries the
// uniqueness, so truncating the readable part loses nothing important.
const ownerIDMaxNameLen = 64

// ownerIDFor is the pure core of testOwnerID: it lowercases the test name,
// replaces every character outside [a-z0-9] with '-', collapses runs of '-',
// truncates to ownerIDMaxNameLen, and joins prefix, name and suffix.
func ownerIDFor(testName, suffix string) string {
	var b strings.Builder
	b.Grow(len(testName))
	lastDash := true // suppress a leading dash
	for _, r := range strings.ToLower(testName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	name := strings.TrimSuffix(b.String(), "-")
	if len(name) > ownerIDMaxNameLen {
		name = strings.TrimSuffix(name[:ownerIDMaxNameLen], "-")
	}
	if name == "" {
		return ownerIDPrefix + "-" + suffix
	}
	return ownerIDPrefix + "-" + name + "-" + suffix
}

// latestEmbeddedMigrationVersion scans the embedded migrations directory and
// returns the highest NNNNNN sequence that has an .up.sql file. The gated
// smoke test compares it against schema_migrations to prove the runner
// applied everything that ships in the binary.
func latestEmbeddedMigrationVersion() (uint, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return 0, fmt.Errorf("reading embedded migrations: %w", err)
	}
	var latest uint
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		seq, _, ok := strings.Cut(name, "_")
		if !ok {
			return 0, fmt.Errorf("migration %q does not follow NNNNNN_name.up.sql", name)
		}
		v, err := strconv.ParseUint(seq, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("migration %q has non-numeric sequence: %w", name, err)
		}
		if uint(v) > latest {
			latest = uint(v)
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no *.up.sql files found in embedded migrations")
	}
	return latest, nil
}

// testLogger discards output so migration chatter does not drown test logs;
// failures surface through returned errors, not through the logger.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---------------------------------------------------------------------------
// Hermetic tests — always on, no database required.
// ---------------------------------------------------------------------------

func TestTestDatabaseURL_Gating(t *testing.T) {
	tests := []struct {
		name   string
		lookup func(string) (string, bool)
		want   string
	}{
		{
			name:   "unset → empty (skip)",
			lookup: func(string) (string, bool) { return "", false },
			want:   "",
		},
		{
			name:   "set but empty → empty (skip)",
			lookup: func(string) (string, bool) { return "", true },
			want:   "",
		},
		{
			name:   "whitespace only → empty (skip)",
			lookup: func(string) (string, bool) { return "  \n", true },
			want:   "",
		},
		{
			name: "set → returned verbatim",
			lookup: func(k string) (string, bool) {
				if k == testDatabaseURLEnv {
					return "postgresql://u:p@localhost:5432/t?sslmode=disable", true
				}
				return "", false
			},
			want: "postgresql://u:p@localhost:5432/t?sslmode=disable",
		},
		{
			// The production variable must never be consulted, even when
			// it is the only one present.
			name: "runtime KUBECENTER_DATABASE_URL is ignored",
			lookup: func(k string) (string, bool) {
				if k == "KUBECENTER_DATABASE_URL" {
					return "postgresql://prod:prod@db.internal:5432/kubecenter", true
				}
				return "", false
			},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := testDatabaseURL(tc.lookup); got != tc.want {
				t.Fatalf("testDatabaseURL() = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestOwnerIDFor_Sanitizes(t *testing.T) {
	tests := []struct {
		name     string
		testName string
		suffix   string
		want     string
	}{
		{"plain", "TestFoo", "abcd", "test-testfoo-abcd"},
		{"subtest slash", "TestFoo/bar_baz", "abcd", "test-testfoo-bar-baz-abcd"},
		{"duplicate subtest marker", "TestFoo/case#01", "abcd", "test-testfoo-case-01-abcd"},
		{"spaces and unicode", "TestFoo/über  wide", "abcd", "test-testfoo-ber-wide-abcd"},
		{"leading and trailing junk", "//TestFoo//", "abcd", "test-testfoo-abcd"},
		{"empty name", "", "abcd", "test-abcd"},
		{"only junk", "///", "abcd", "test-abcd"},
		{
			"long name truncated to cap",
			"Test" + strings.Repeat("x", 100),
			"abcd",
			"test-" + ("test" + strings.Repeat("x", 100))[:ownerIDMaxNameLen] + "-abcd",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownerIDFor(tc.testName, tc.suffix); got != tc.want {
				t.Fatalf("ownerIDFor(%q, %q) = %q; want %q", tc.testName, tc.suffix, got, tc.want)
			}
		})
	}
}

func TestTestOwnerID_UniqueAndPrefixed(t *testing.T) {
	const iterations = 256
	// ownerIDFor with an empty suffix yields exactly "<prefix>-<name>-", i.e.
	// the deterministic part every id from this invocation must start with.
	wantPrefix := ownerIDFor(t.Name(), "")

	seen := make(map[string]struct{}, iterations)
	for range iterations {
		id := testOwnerID(t)
		if !strings.HasPrefix(id, wantPrefix) {
			t.Fatalf("owner id %q lacks deterministic prefix %q", id, wantPrefix)
		}
		suffix := strings.TrimPrefix(id, wantPrefix)
		if len(suffix) != 16 {
			t.Fatalf("owner id %q suffix %q is %d chars; want 16 hex chars", id, suffix, len(suffix))
		}
		if _, err := hex.DecodeString(suffix); err != nil {
			t.Fatalf("owner id %q suffix %q is not hex: %v", id, suffix, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("owner id %q generated twice", id)
		}
		seen[id] = struct{}{}
	}

	// Subtests must get a distinct prefix from their parent so rows are
	// attributable to the exact invocation that wrote them.
	t.Run("subtest", func(t *testing.T) {
		id := testOwnerID(t)
		subPrefix := ownerIDFor(t.Name(), "")
		if subPrefix == wantPrefix {
			t.Fatalf("subtest prefix %q is identical to the parent's", subPrefix)
		}
		if !strings.HasPrefix(id, subPrefix) {
			t.Fatalf("subtest owner id %q lacks prefix %q", id, subPrefix)
		}
	})
}

func TestLatestEmbeddedMigrationVersion(t *testing.T) {
	got, err := latestEmbeddedMigrationVersion()
	if err != nil {
		t.Fatalf("latestEmbeddedMigrationVersion() error: %v", err)
	}
	// 000017 is the last migration at the time this harness landed; the
	// sequence only ever grows (G4: pre-assigned, never renumbered).
	if got < 17 {
		t.Fatalf("latest embedded migration = %d; want >= 17", got)
	}

	// Every up migration must have a matching down migration — the runner
	// cannot roll back a version that is missing its pair.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("reading embedded migrations: %v", err)
	}
	names := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		names[e.Name()] = struct{}{}
	}
	for name := range names {
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		down := strings.TrimSuffix(name, ".up.sql") + ".down.sql"
		if _, ok := names[down]; !ok {
			t.Errorf("migration %s has no matching %s", name, down)
		}
	}
}

// ---------------------------------------------------------------------------
// Gated smoke test — runs only when KUBECENTER_TEST_DATABASE_URL is set.
// ---------------------------------------------------------------------------

func TestDBHarness_MigrationsApplied(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// golang-migrate's postgres driver records progress in schema_migrations
	// (version BIGINT, dirty BOOLEAN) — a single row once Up() completes.
	var (
		version int64
		dirty   bool
	)
	if err := pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatalf("reading schema_migrations: %v", err)
	}
	if dirty {
		t.Fatalf("schema_migrations reports dirty state at version %d", version)
	}

	want, err := latestEmbeddedMigrationVersion()
	if err != nil {
		t.Fatalf("latestEmbeddedMigrationVersion() error: %v", err)
	}
	if uint(version) != want {
		t.Fatalf("schema_migrations.version = %d; want latest embedded migration %d", version, want)
	}

	// Spot-check that a table from the first migration is queryable, which
	// proves the pool is pointed at the schema the runner just built and
	// not at a different database on the same server.
	var n int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs`).Scan(&n); err != nil {
		t.Fatalf("querying audit_logs: %v", err)
	}
	if n < 0 {
		t.Fatalf("audit_logs count = %d; want >= 0", n)
	}
}

// TestDBHarness_PerTestPoolsAreIndependent verifies that two tests calling
// testDB get separate pools (so one test's Close cannot break another) and
// that both see the same migrated schema.
func TestDBHarness_PerTestPoolsAreIndependent(t *testing.T) {
	first := testDB(t)
	var second *pgxpool.Pool
	t.Run("inner", func(t *testing.T) {
		second = testDB(t)
		if second == first {
			t.Fatal("nested testDB call returned the parent's pool; want a private pool")
		}
		if err := second.Ping(t.Context()); err != nil {
			t.Fatalf("inner pool ping: %v", err)
		}
	})
	// The inner test's cleanup has closed its pool; the outer pool must be
	// unaffected.
	if err := first.Ping(t.Context()); err != nil {
		t.Fatalf("outer pool ping after inner cleanup: %v", err)
	}
}
