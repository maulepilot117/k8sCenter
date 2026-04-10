package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

// Store handles PostgreSQL CRUD for the notification center tables.
type Store struct {
	pool         *pgxpool.Pool
	masterSecret string
}

// NewStore creates a notification store backed by PostgreSQL.
func NewStore(pool *pgxpool.Pool, masterSecret string) *Store {
	return &Store{pool: pool, masterSecret: masterSecret}
}

// --- Notifications ---

// InsertNotification persists a notification and returns its ID.
func (s *Store) InsertNotification(ctx context.Context, n Notification) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO nc_notifications (source, severity, title, message, resource_kind, resource_ns, resource_name, cluster_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		n.Source, n.Severity, n.Title, n.Message,
		n.ResourceKind, n.ResourceNS, n.ResourceName, n.ClusterID,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert notification: %w", err)
	}
	return id, nil
}

// DedupExists checks whether a matching notification was created within the dedup window.
// Uses database time (now()) to avoid clock drift between app server and PostgreSQL.
func (s *Store) DedupExists(ctx context.Context, n Notification, window time.Duration) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM nc_notifications
			WHERE source = $1 AND resource_kind = $2 AND resource_ns = $3
			  AND resource_name = $4 AND title = $5
			  AND created_at > now() - $6::interval
		)`,
		n.Source, n.ResourceKind, n.ResourceNS, n.ResourceName, n.Title,
		fmt.Sprintf("%d seconds", int(window.Seconds())),
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("dedup check: %w", err)
	}
	return exists, nil
}

// ListNotifications returns paginated, RBAC-filtered notifications for a user.
func (s *Store) ListNotifications(ctx context.Context, opts ListOpts) ([]Notification, int, error) {
	if opts.Limit <= 0 {
		opts.Limit = 25
	}
	if opts.Limit > 200 {
		opts.Limit = 200
	}

	args := []any{opts.UserID}
	where := "WHERE 1=1"
	argIdx := 2

	if len(opts.Namespaces) > 0 {
		where += fmt.Sprintf(" AND (n.resource_ns = ANY($%d) OR n.resource_ns = '')", argIdx)
		args = append(args, opts.Namespaces)
		argIdx++
	}
	if opts.Source != "" {
		where += fmt.Sprintf(" AND n.source = $%d", argIdx)
		args = append(args, string(opts.Source))
		argIdx++
	}
	if opts.Severity != "" {
		where += fmt.Sprintf(" AND n.severity = $%d", argIdx)
		args = append(args, string(opts.Severity))
		argIdx++
	}
	if !opts.Since.IsZero() {
		where += fmt.Sprintf(" AND n.created_at >= $%d", argIdx)
		args = append(args, opts.Since)
		argIdx++
	}
	if !opts.Until.IsZero() {
		where += fmt.Sprintf(" AND n.created_at <= $%d", argIdx)
		args = append(args, opts.Until)
		argIdx++
	}
	if opts.ReadFilter == "read" {
		where += " AND nr.notification_id IS NOT NULL"
	} else if opts.ReadFilter == "unread" {
		where += " AND nr.notification_id IS NULL"
	}

	countQuery := fmt.Sprintf(`
		SELECT COUNT(*) FROM nc_notifications n
		LEFT JOIN nc_reads nr ON nr.notification_id = n.id AND nr.user_id = $1
		%s`, where)
	var total int
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count notifications: %w", err)
	}

	listQuery := fmt.Sprintf(`
		SELECT n.id, n.source, n.severity, n.title, n.message,
		       n.resource_kind, n.resource_ns, n.resource_name, n.cluster_id, n.created_at,
		       (nr.notification_id IS NOT NULL) AS read
		FROM nc_notifications n
		LEFT JOIN nc_reads nr ON nr.notification_id = n.id AND nr.user_id = $1
		%s
		ORDER BY n.created_at DESC
		LIMIT $%d OFFSET $%d`,
		where, argIdx, argIdx+1)
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.pool.Query(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(
			&n.ID, &n.Source, &n.Severity, &n.Title, &n.Message,
			&n.ResourceKind, &n.ResourceNS, &n.ResourceName, &n.ClusterID, &n.CreatedAt,
			&n.Read,
		); err != nil {
			return nil, 0, fmt.Errorf("scan notification: %w", err)
		}
		notifications = append(notifications, n)
	}
	return notifications, total, rows.Err()
}

// UnreadCount returns the number of unread notifications for a user.
func (s *Store) UnreadCount(ctx context.Context, userID string, namespaces []string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM nc_notifications n
		LEFT JOIN nc_reads nr ON nr.notification_id = n.id AND nr.user_id = $1
		WHERE nr.notification_id IS NULL
		  AND n.created_at > now() - INTERVAL '30 days'
		  AND (n.resource_ns = ANY($2) OR n.resource_ns = '')`,
		userID, namespaces,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("unread count: %w", err)
	}
	return count, nil
}

// MarkRead marks a single notification as read for a user.
func (s *Store) MarkRead(ctx context.Context, userID, notificationID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO nc_reads (user_id, notification_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`,
		userID, notificationID)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// MarkAllRead marks all notifications as read for a user (batched to avoid WAL pressure).
func (s *Store) MarkAllRead(ctx context.Context, userID string) error {
	for {
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO nc_reads (user_id, notification_id)
			SELECT $1, n.id FROM nc_notifications n
			LEFT JOIN nc_reads nr ON nr.notification_id = n.id AND nr.user_id = $1
			WHERE nr.notification_id IS NULL
			  AND n.created_at > now() - INTERVAL '90 days'
			LIMIT 5000`,
			userID)
		if err != nil {
			return fmt.Errorf("mark all read: %w", err)
		}
		if tag.RowsAffected() == 0 {
			break
		}
	}
	return nil
}

// PruneOlderThan deletes notifications older than the given duration. Returns count deleted.
func (s *Store) PruneOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM nc_notifications WHERE created_at < now() - $1::interval",
		fmt.Sprintf("%d seconds", int(age.Seconds())))
	if err != nil {
		return 0, fmt.Errorf("prune notifications: %w", err)
	}
	return tag.RowsAffected(), nil
}

// NotificationsSince returns notifications matching filters created after the given time.
// Includes namespace filtering for RBAC compliance.
func (s *Store) NotificationsSince(ctx context.Context, since time.Time, namespaces []string, sourceFilter []string, severityFilter []string) ([]Notification, error) {
	query := `
		SELECT id, source, severity, title, message,
		       resource_kind, resource_ns, resource_name, cluster_id, created_at
		FROM nc_notifications
		WHERE created_at > $1`
	args := []any{since}
	argIdx := 2

	if len(namespaces) > 0 {
		query += fmt.Sprintf(" AND (resource_ns = ANY($%d) OR resource_ns = '')", argIdx)
		args = append(args, namespaces)
		argIdx++
	}
	if len(sourceFilter) > 0 {
		query += fmt.Sprintf(" AND source = ANY($%d)", argIdx)
		args = append(args, sourceFilter)
		argIdx++
	}
	if len(severityFilter) > 0 {
		query += fmt.Sprintf(" AND severity = ANY($%d)", argIdx)
		args = append(args, severityFilter)
	}
	query += " ORDER BY created_at DESC LIMIT 500"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("notifications since: %w", err)
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(
			&n.ID, &n.Source, &n.Severity, &n.Title, &n.Message,
			&n.ResourceKind, &n.ResourceNS, &n.ResourceName, &n.ClusterID, &n.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		notifications = append(notifications, n)
	}
	return notifications, rows.Err()
}

// --- Channels ---

// channelScanner is satisfied by both pgx.Row and pgx.Rows.
type channelScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanChannelFrom(sc channelScanner) (Channel, error) {
	var ch Channel
	var configBytes []byte
	if err := sc.Scan(
		&ch.ID, &ch.Name, &ch.Type, &configBytes, &ch.CreatedBy, &ch.CreatedAt,
		&ch.UpdatedAt, &ch.UpdatedBy, &ch.LastSentAt, &ch.LastError, &ch.LastErrorAt,
	); err != nil {
		return Channel{}, fmt.Errorf("scan channel: %w", err)
	}
	cfg, err := s.decryptConfig(configBytes)
	if err != nil {
		return Channel{}, err
	}
	ch.Config = cfg
	return ch, nil
}

const channelColumns = `id, name, type, config, created_by, created_at,
	updated_at, updated_by, last_sent_at, last_error, last_error_at`

// ListChannels returns all notification channels.
func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+channelColumns+`
		FROM nc_channels
		ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		ch, err := s.scanChannelFrom(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// GetChannel returns a single channel by ID.
func (s *Store) GetChannel(ctx context.Context, id string) (Channel, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+channelColumns+`
		FROM nc_channels WHERE id = $1`, id)
	ch, err := s.scanChannelFrom(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	return ch, err
}

// CreateChannel creates a new channel with encrypted config.
func (s *Store) CreateChannel(ctx context.Context, ch Channel) (string, error) {
	configBytes, err := s.encryptConfig(ch.Config)
	if err != nil {
		return "", err
	}

	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO nc_channels (name, type, config, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		ch.Name, ch.Type, configBytes, ch.CreatedBy,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create channel: %w", err)
	}
	return id, nil
}

// UpdateChannel updates a channel's name and config. Returns ErrNotFound if ID doesn't exist.
func (s *Store) UpdateChannel(ctx context.Context, ch Channel) error {
	configBytes, err := s.encryptConfig(ch.Config)
	if err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE nc_channels SET name = $1, config = $2, updated_at = now(), updated_by = $3
		WHERE id = $4`,
		ch.Name, configBytes, ch.UpdatedBy, ch.ID)
	if err != nil {
		return fmt.Errorf("update channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteChannel deletes a channel (cascades to rules). Returns ErrNotFound if ID doesn't exist.
func (s *Store) DeleteChannel(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM nc_channels WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateChannelError records a dispatch error on a channel.
func (s *Store) UpdateChannelError(ctx context.Context, id, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE nc_channels SET last_error = $1, last_error_at = now() WHERE id = $2`,
		errMsg, id)
	if err != nil {
		return fmt.Errorf("update channel error: %w", err)
	}
	return nil
}

// UpdateChannelLastSent updates the last_sent_at timestamp and clears errors for digest tracking.
func (s *Store) UpdateChannelLastSent(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE nc_channels SET last_sent_at = now(), last_error = NULL, last_error_at = NULL WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("update channel last sent: %w", err)
	}
	return nil
}

// --- Rules ---

// sourcesToStrings converts a Source slice to a string slice for PostgreSQL TEXT[] columns.
func sourcesToStrings(sources []Source) []string {
	out := make([]string, len(sources))
	for i, v := range sources {
		out[i] = string(v)
	}
	return out
}

// severitiesToStrings converts a Severity slice to a string slice for PostgreSQL TEXT[] columns.
func severitiesToStrings(severities []Severity) []string {
	out := make([]string, len(severities))
	for i, v := range severities {
		out[i] = string(v)
	}
	return out
}

// ListRules returns all notification rules with channel names.
func (s *Store) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, r.source_filter, r.severity_filter, r.channel_id,
		       c.name, r.enabled, r.created_by, r.created_at, r.updated_at, r.updated_by
		FROM nc_rules r
		INNER JOIN nc_channels c ON c.id = r.channel_id
		ORDER BY r.created_at`)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(
			&r.ID, &r.Name, &r.SourceFilter, &r.SeverityFilter, &r.ChannelID,
			&r.ChannelName, &r.Enabled, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &r.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// CreateRule creates a new routing rule.
func (s *Store) CreateRule(ctx context.Context, r Rule) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO nc_rules (name, source_filter, severity_filter, channel_id, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		r.Name, sourcesToStrings(r.SourceFilter), severitiesToStrings(r.SeverityFilter),
		r.ChannelID, r.Enabled, r.CreatedBy,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create rule: %w", err)
	}
	return id, nil
}

// UpdateRule updates a rule's filters, channel, and enabled state. Returns ErrNotFound if ID doesn't exist.
func (s *Store) UpdateRule(ctx context.Context, r Rule) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE nc_rules
		SET name = $1, source_filter = $2, severity_filter = $3, channel_id = $4,
		    enabled = $5, updated_at = now(), updated_by = $6
		WHERE id = $7`,
		r.Name, sourcesToStrings(r.SourceFilter), severitiesToStrings(r.SeverityFilter),
		r.ChannelID, r.Enabled, r.UpdatedBy, r.ID)
	if err != nil {
		return fmt.Errorf("update rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRule deletes a routing rule. Returns ErrNotFound if ID doesn't exist.
func (s *Store) DeleteRule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM nc_rules WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Helpers ---

func (s *Store) encryptConfig(cfg ChannelConfig) ([]byte, error) {
	plaintext, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal channel config: %w", err)
	}
	encrypted, err := store.Encrypt(plaintext, s.masterSecret)
	if err != nil {
		return nil, fmt.Errorf("encrypt channel config: %w", err)
	}
	return encrypted, nil
}

func (s *Store) decryptConfig(encrypted []byte) (ChannelConfig, error) {
	plaintext, err := store.Decrypt(encrypted, s.masterSecret)
	if err != nil {
		return nil, fmt.Errorf("decrypt channel config: %w", err)
	}
	var cfg ChannelConfig
	if err := json.Unmarshal(plaintext, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal channel config: %w", err)
	}
	return cfg, nil
}
