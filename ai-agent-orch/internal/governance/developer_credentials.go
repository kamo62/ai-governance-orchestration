package governance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

const defaultDeveloperRuntimeCredentialTTL = 90 * 24 * time.Hour

type DeveloperCredentialIssue struct {
	ActorSubject string
	Client       string
	DeviceName   string
	Now          time.Time
	TTL          time.Duration
}

type DeveloperRuntimeCredential struct {
	ID             string    `json:"id"`
	ActorSubject   string    `json:"actor_subject"`
	Client         string    `json:"client"`
	DeviceNameHash string    `json:"device_name_hash,omitempty"`
	IssuedAt       time.Time `json:"issued_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	RevokedAt      time.Time `json:"revoked_at,omitempty"`
	LastUsedAt     time.Time `json:"last_used_at,omitempty"`
}

type DeveloperCredentialStore interface {
	Issue(ctx context.Context, issue DeveloperCredentialIssue) (DeveloperRuntimeCredential, string, error)
	Validate(ctx context.Context, token string, now time.Time) (DeveloperRuntimeCredential, bool, error)
}

type SQLiteDeveloperCredentialStore struct {
	db *sql.DB
}

func NewSQLiteDeveloperCredentialStore(dbPath string) (*SQLiteDeveloperCredentialStore, error) {
	if dbPath == "" {
		return nil, errors.New("sqlite developer credential db path is required")
	}
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, fmt.Errorf("create developer credential db directory: %w", err)
		}
	}
	db, err := sqlitex.Open(dbPath, "developer credential")
	if err != nil {
		return nil, err
	}
	store := &SQLiteDeveloperCredentialStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate developer credentials: %w", err)
	}
	if dbPath != ":memory:" {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set developer credential db permissions: %w", err)
		}
	}
	return store, nil
}

func (s *SQLiteDeveloperCredentialStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteDeveloperCredentialStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS developer_runtime_credentials (
			id TEXT PRIMARY KEY,
			actor_subject TEXT NOT NULL,
			client TEXT NOT NULL,
			device_name_hash TEXT NOT NULL,
			token_sha256 TEXT NOT NULL UNIQUE,
			issued_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT,
			last_used_at TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_developer_runtime_credentials_actor ON developer_runtime_credentials(actor_subject);
		CREATE INDEX IF NOT EXISTS idx_developer_runtime_credentials_token ON developer_runtime_credentials(token_sha256);
	`)
	return err
}

func (s *SQLiteDeveloperCredentialStore) Issue(ctx context.Context, issue DeveloperCredentialIssue) (DeveloperRuntimeCredential, string, error) {
	if s == nil || s.db == nil {
		return DeveloperRuntimeCredential{}, "", errors.New("developer credential store unavailable")
	}
	actor := strings.TrimSpace(issue.ActorSubject)
	if !validActorLabel(actor) {
		return DeveloperRuntimeCredential{}, "", errors.New("valid actor subject is required")
	}
	client := sanitizeCredentialLabel(issue.Client, "opencode")
	now := issue.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	ttl := issue.TTL
	if ttl <= 0 {
		ttl = defaultDeveloperRuntimeCredentialTTL
	}
	if ttl < defaultDeveloperRuntimeCredentialTTL {
		ttl = defaultDeveloperRuntimeCredentialTTL
	}
	token, err := randomDeveloperRuntimeToken()
	if err != nil {
		return DeveloperRuntimeCredential{}, "", err
	}
	rec := DeveloperRuntimeCredential{
		ID:             randomCredentialID(token),
		ActorSubject:   actor,
		Client:         client,
		DeviceNameHash: hashTrimmed(issue.DeviceName),
		IssuedAt:       now,
		ExpiresAt:      now.Add(ttl),
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO developer_runtime_credentials (
			id, actor_subject, client, device_name_hash, token_sha256, issued_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, rec.ID, rec.ActorSubject, rec.Client, rec.DeviceNameHash, sha256HexString(token), rec.IssuedAt.Format(time.RFC3339Nano), rec.ExpiresAt.Format(time.RFC3339Nano))
	if err != nil {
		return DeveloperRuntimeCredential{}, "", fmt.Errorf("insert developer runtime credential: %w", err)
	}
	return rec, token, nil
}

func (s *SQLiteDeveloperCredentialStore) Validate(ctx context.Context, token string, now time.Time) (DeveloperRuntimeCredential, bool, error) {
	if s == nil || s.db == nil {
		return DeveloperRuntimeCredential{}, false, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return DeveloperRuntimeCredential{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var rec DeveloperRuntimeCredential
	var issuedAt, expiresAt string
	var revokedAt sql.NullString
	var lastUsedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, actor_subject, client, device_name_hash, issued_at, expires_at, revoked_at, last_used_at
		FROM developer_runtime_credentials WHERE token_sha256 = ?
	`, sha256HexString(token)).Scan(&rec.ID, &rec.ActorSubject, &rec.Client, &rec.DeviceNameHash, &issuedAt, &expiresAt, &revokedAt, &lastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DeveloperRuntimeCredential{}, false, nil
	}
	if err != nil {
		return DeveloperRuntimeCredential{}, false, fmt.Errorf("lookup developer runtime credential: %w", err)
	}
	rec.IssuedAt, _ = time.Parse(time.RFC3339Nano, issuedAt)
	rec.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
	if revokedAt.Valid && strings.TrimSpace(revokedAt.String) != "" {
		rec.RevokedAt, _ = time.Parse(time.RFC3339Nano, revokedAt.String)
		return rec, false, nil
	}
	if !rec.ExpiresAt.IsZero() && now.UTC().After(rec.ExpiresAt) {
		return rec, false, nil
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE developer_runtime_credentials SET last_used_at = ? WHERE id = ?`, now.UTC().Format(time.RFC3339Nano), rec.ID)
	rec.LastUsedAt = now.UTC()
	return rec, true, nil
}

func sanitizeCredentialLabel(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

func randomDeveloperRuntimeToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate developer runtime token: %w", err)
	}
	return "air_" + hex.EncodeToString(buf), nil
}

func randomCredentialID(token string) string {
	h := sha256.Sum256([]byte(token))
	return "drc_" + hex.EncodeToString(h[:8])
}

func sha256HexString(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func hashTrimmed(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return sha256HexString(strings.ToLower(value))
}
