package copilot

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

const ProviderID = "github-copilot"

type TokenRecord struct {
	ActorSubject     string
	GitHubLogin      string
	GitHubUserID     string
	BaseURL          string
	AccessToken      string
	RefreshToken     string
	Fingerprint      string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	RevokedAt        *time.Time
}

type Store struct {
	db  *sql.DB
	key []byte
}

func DefaultStorePath() string {
	if path := strings.TrimSpace(os.Getenv("AI_ORCH_COPILOT_TOKEN_DB")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "copilot-tokens.db"
	}
	return filepath.Join(home, ".ai-orch", "copilot-tokens.db")
}

func OpenStore(path string, key string) (*Store, error) {
	if path == "" {
		path = DefaultStorePath()
	}
	if key == "" {
		key = os.Getenv("AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY")
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY is required")
	}
	db, err := sqlitex.Open(path, "copilot token")
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, key: normalizeKey(key)}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS copilot_user_tokens (
			actor_subject TEXT PRIMARY KEY,
			github_login TEXT NOT NULL,
			github_user_id TEXT NOT NULL,
			copilot_base_url TEXT NOT NULL,
			access_token_ciphertext BLOB NOT NULL,
			refresh_token_ciphertext BLOB,
			token_fingerprint TEXT NOT NULL,
			access_expires_at TEXT NOT NULL DEFAULT '',
			refresh_expires_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			revoked_at TEXT
		);
	`)
	if err != nil {
		return err
	}
	cols := map[string]string{
		"refresh_token_ciphertext": "BLOB",
		"access_expires_at":        "TEXT NOT NULL DEFAULT ''",
		"refresh_expires_at":       "TEXT NOT NULL DEFAULT ''",
	}
	for col, def := range cols {
		if err := s.ensureColumn(col, def); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureColumn(column string, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(copilot_user_tokens)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE copilot_user_tokens ADD COLUMN %s %s`, column, definition)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return nil
		}
		return err
	}
	return nil
}

func (s *Store) Save(ctx context.Context, rec TokenRecord) error {
	if s == nil || s.db == nil {
		return errors.New("copilot token store unavailable")
	}
	if rec.ActorSubject == "" || rec.AccessToken == "" {
		return errors.New("actor_subject and access token are required")
	}
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	if rec.BaseURL == "" {
		rec.BaseURL = DefaultCopilotBaseURL
	}
	accessCiphertext, err := encrypt(s.key, []byte(rec.AccessToken))
	if err != nil {
		return err
	}
	var refreshCiphertext []byte
	if strings.TrimSpace(rec.RefreshToken) != "" {
		refreshCiphertext, err = encrypt(s.key, []byte(rec.RefreshToken))
		if err != nil {
			return err
		}
	}
	rec.Fingerprint = Fingerprint(rec.AccessToken)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO copilot_user_tokens (
			actor_subject, github_login, github_user_id, copilot_base_url,
			access_token_ciphertext, refresh_token_ciphertext, token_fingerprint,
			access_expires_at, refresh_expires_at, created_at, updated_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(actor_subject) DO UPDATE SET
			github_login=excluded.github_login,
			github_user_id=excluded.github_user_id,
			copilot_base_url=excluded.copilot_base_url,
			access_token_ciphertext=excluded.access_token_ciphertext,
			refresh_token_ciphertext=excluded.refresh_token_ciphertext,
			token_fingerprint=excluded.token_fingerprint,
			access_expires_at=excluded.access_expires_at,
			refresh_expires_at=excluded.refresh_expires_at,
			updated_at=excluded.updated_at,
			revoked_at=NULL
	`, rec.ActorSubject, rec.GitHubLogin, rec.GitHubUserID, rec.BaseURL, accessCiphertext, refreshCiphertext, rec.Fingerprint,
		formatOptionalTime(rec.AccessExpiresAt), formatOptionalTime(rec.RefreshExpiresAt), rec.CreatedAt.Format(time.RFC3339Nano), rec.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) Load(ctx context.Context, actorSubject string) (TokenRecord, error) {
	var rec TokenRecord
	var accessCiphertext []byte
	var refreshCiphertext []byte
	var created, updated string
	var accessExpires, refreshExpires string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT actor_subject, github_login, github_user_id, copilot_base_url,
		       access_token_ciphertext, COALESCE(refresh_token_ciphertext, ''), token_fingerprint,
		       COALESCE(access_expires_at, ''), COALESCE(refresh_expires_at, ''), created_at, updated_at, revoked_at
		FROM copilot_user_tokens WHERE actor_subject = ?
	`, actorSubject).Scan(&rec.ActorSubject, &rec.GitHubLogin, &rec.GitHubUserID, &rec.BaseURL, &accessCiphertext, &refreshCiphertext, &rec.Fingerprint, &accessExpires, &refreshExpires, &created, &updated, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenRecord{}, ErrTokenNotFound
	}
	if err != nil {
		return TokenRecord{}, err
	}
	plain, err := decrypt(s.key, accessCiphertext)
	if err != nil {
		return TokenRecord{}, err
	}
	rec.AccessToken = string(plain)
	if len(refreshCiphertext) > 0 {
		refreshPlain, err := decrypt(s.key, refreshCiphertext)
		if err != nil {
			return TokenRecord{}, err
		}
		rec.RefreshToken = string(refreshPlain)
	}
	rec.AccessExpiresAt = parseOptionalTime(accessExpires)
	rec.RefreshExpiresAt = parseOptionalTime(refreshExpires)
	rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if revoked.Valid {
		if t, err := time.Parse(time.RFC3339Nano, revoked.String); err == nil {
			rec.RevokedAt = &t
		}
	}
	if rec.RevokedAt != nil {
		return TokenRecord{}, ErrTokenRevoked
	}
	return rec, nil
}

func (s *Store) TokenForActor(ctx context.Context, actorSubject string) (TokenRecord, error) {
	return s.Load(ctx, actorSubject)
}

func (s *Store) UpdateOAuthToken(ctx context.Context, actorSubject string, token AccessTokenResponse, now time.Time) (TokenRecord, error) {
	rec, err := s.Load(ctx, actorSubject)
	if err != nil {
		return TokenRecord{}, err
	}
	if token.AccessToken == "" {
		return TokenRecord{}, errors.New("access token is required")
	}
	rec.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		rec.RefreshToken = token.RefreshToken
	}
	rec.AccessExpiresAt = token.AccessExpiresAt(now)
	if refreshExpires := token.RefreshExpiresAt(now); !refreshExpires.IsZero() {
		rec.RefreshExpiresAt = refreshExpires
	}
	if err := s.Save(ctx, rec); err != nil {
		return TokenRecord{}, err
	}
	return s.Load(ctx, actorSubject)
}

// EnrollmentCount reports how many actor enrollments the store holds. Logged
// at startup so an unexpectedly emptied store is visible immediately.
func (s *Store) EnrollmentCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM copilot_user_tokens`).Scan(&n)
	return n, err
}

func (s *Store) Delete(ctx context.Context, actorSubject string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM copilot_user_tokens WHERE actor_subject = ?`, actorSubject)
	return err
}

func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:8])
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseOptionalTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func normalizeKey(key string) []byte {
	if decoded, err := base64.StdEncoding.DecodeString(key); err == nil && len(decoded) >= 16 {
		sum := sha256.Sum256(decoded)
		return sum[:]
	}
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

func encrypt(key []byte, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	return append(nonce, sealed...), nil
}

func decrypt(key []byte, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	sealed := ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, sealed, nil)
}

var ErrTokenNotFound = errors.New("copilot token not found")
var ErrTokenRevoked = errors.New("copilot token revoked")
