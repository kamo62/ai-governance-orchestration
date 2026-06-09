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

	_ "modernc.org/sqlite"
)

const ProviderID = "github-copilot"

type TokenRecord struct {
	ActorSubject string
	GitHubLogin  string
	GitHubUserID string
	BaseURL      string
	AccessToken  string
	Fingerprint  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	RevokedAt    *time.Time
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create copilot token dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open copilot token db: %w", err)
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
			token_fingerprint TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			revoked_at TEXT
		);
	`)
	return err
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
	ciphertext, err := encrypt(s.key, []byte(rec.AccessToken))
	if err != nil {
		return err
	}
	rec.Fingerprint = Fingerprint(rec.AccessToken)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO copilot_user_tokens (
			actor_subject, github_login, github_user_id, copilot_base_url,
			access_token_ciphertext, token_fingerprint, created_at, updated_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(actor_subject) DO UPDATE SET
			github_login=excluded.github_login,
			github_user_id=excluded.github_user_id,
			copilot_base_url=excluded.copilot_base_url,
			access_token_ciphertext=excluded.access_token_ciphertext,
			token_fingerprint=excluded.token_fingerprint,
			updated_at=excluded.updated_at,
			revoked_at=NULL
	`, rec.ActorSubject, rec.GitHubLogin, rec.GitHubUserID, rec.BaseURL, ciphertext, rec.Fingerprint, rec.CreatedAt.Format(time.RFC3339Nano), rec.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) Load(ctx context.Context, actorSubject string) (TokenRecord, error) {
	var rec TokenRecord
	var ciphertext []byte
	var created, updated string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT actor_subject, github_login, github_user_id, copilot_base_url,
		       access_token_ciphertext, token_fingerprint, created_at, updated_at, revoked_at
		FROM copilot_user_tokens WHERE actor_subject = ?
	`, actorSubject).Scan(&rec.ActorSubject, &rec.GitHubLogin, &rec.GitHubUserID, &rec.BaseURL, &ciphertext, &rec.Fingerprint, &created, &updated, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenRecord{}, ErrTokenNotFound
	}
	if err != nil {
		return TokenRecord{}, err
	}
	plain, err := decrypt(s.key, ciphertext)
	if err != nil {
		return TokenRecord{}, err
	}
	rec.AccessToken = string(plain)
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

func (s *Store) Delete(ctx context.Context, actorSubject string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM copilot_user_tokens WHERE actor_subject = ?`, actorSubject)
	return err
}

func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:8])
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
