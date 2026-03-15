package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const createTableSQL = `
CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL,
    cluster_id TEXT NOT NULL,
    user TEXT NOT NULL,
    source_ip TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_kind TEXT DEFAULT '',
    resource_namespace TEXT DEFAULT '',
    resource_name TEXT DEFAULT '',
    result TEXT NOT NULL,
    detail TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_logs(timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(user);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_logs(action);
`

// SQLiteStore provides persistent audit log storage backed by SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (or creates) a SQLite database at the given path
// and initializes the schema. Uses WAL mode for concurrent read/write.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening audit database: %w", err)
	}

	// Enable WAL mode and set busy timeout for concurrent access
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting busy timeout: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	// Limit WAL file growth
	if _, err := db.Exec("PRAGMA wal_autocheckpoint=1000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting WAL autocheckpoint: %w", err)
	}

	// Run schema migration
	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating audit table: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// Insert writes an audit entry to the database.
func (s *SQLiteStore) Insert(ctx context.Context, e Entry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_logs (timestamp, cluster_id, user, source_ip, action, resource_kind, resource_namespace, resource_name, result, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Timestamp.UTC().Format(time.RFC3339Nano),
		e.ClusterID, e.User, e.SourceIP, string(e.Action),
		e.ResourceKind, e.ResourceNamespace, e.ResourceName,
		string(e.Result), e.Detail,
	)
	if err != nil {
		return fmt.Errorf("inserting audit entry: %w", err)
	}
	return nil
}

// Query returns audit entries matching the given filters with pagination.
// Returns entries, total count, and error.
func (s *SQLiteStore) Query(ctx context.Context, params QueryParams) ([]Entry, int, error) {
	params.Normalize()

	var where []string
	var args []any

	if params.User != "" {
		where = append(where, "user = ?")
		args = append(args, params.User)
	}
	if params.Action != "" {
		where = append(where, "action = ?")
		args = append(args, params.Action)
	}
	if params.ResourceKind != "" {
		where = append(where, "resource_kind = ?")
		args = append(args, params.ResourceKind)
	}
	if params.Namespace != "" {
		where = append(where, "resource_namespace = ?")
		args = append(args, params.Namespace)
	}
	if !params.Since.IsZero() {
		where = append(where, "timestamp >= ?")
		args = append(args, params.Since.UTC().Format(time.RFC3339Nano))
	}
	if !params.Until.IsZero() {
		where = append(where, "timestamp <= ?")
		args = append(args, params.Until.UTC().Format(time.RFC3339Nano))
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Get total count
	var total int
	countQuery := "SELECT COUNT(*) FROM audit_logs " + whereClause
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting audit entries: %w", err)
	}

	// Get paginated results (newest first)
	dataQuery := fmt.Sprintf(
		"SELECT timestamp, cluster_id, user, source_ip, action, resource_kind, resource_namespace, resource_name, result, detail FROM audit_logs %s ORDER BY timestamp DESC LIMIT ? OFFSET ?",
		whereClause,
	)
	dataArgs := make([]any, 0, len(args)+2)
	dataArgs = append(dataArgs, args...)
	dataArgs = append(dataArgs, params.PageSize, params.Offset())

	rows, err := s.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying audit entries: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		var ts string
		var action, result string
		if err := rows.Scan(&ts, &e.ClusterID, &e.User, &e.SourceIP, &action,
			&e.ResourceKind, &e.ResourceNamespace, &e.ResourceName, &result, &e.Detail); err != nil {
			return nil, 0, fmt.Errorf("scanning audit entry: %w", err)
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		e.Action = Action(action)
		e.Result = Result(result)
		entries = append(entries, e)
	}

	return entries, total, rows.Err()
}

// Cleanup deletes audit entries older than the given number of days.
// Returns the number of deleted rows. Rejects retentionDays < 1 to prevent accidental data loss.
func (s *SQLiteStore) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays < 1 {
		return 0, fmt.Errorf("retentionDays must be >= 1, got %d", retentionDays)
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, "DELETE FROM audit_logs WHERE timestamp < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("cleaning up audit entries: %w", err)
	}
	return result.RowsAffected()
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
