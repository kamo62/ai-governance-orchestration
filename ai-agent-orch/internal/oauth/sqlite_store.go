package oauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

// SQLiteTokenStore persists user OAuth tokens encrypted at rest so MCP
// oauth-user grants survive a Governance Shell restart. Token material is
// AES-GCM encrypted with a key derived from the configured secret; the
// database never sees plaintext tokens.
type SQLiteTokenStore struct {
	db  *sql.DB
	key []byte
}

func NewSQLiteTokenStore(dbPath string, key string) (*SQLiteTokenStore, error) {
	if dbPath == "" {
		return nil, errors.New("sqlite oauth token db path is required")
	}
	if key == "" {
		return nil, errors.New("oauth token encryption key is required")
	}
	db, err := sqlitex.Open(dbPath, "oauth token")
	if err != nil {
		return nil, err
	}
	store := &SQLiteTokenStore{db: db, key: deriveKey(key)}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteTokenStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS oauth_user_tokens (
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			token_ciphertext BLOB NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (user_id, provider)
		);
	`)
	if err != nil {
		return fmt.Errorf("migrate oauth token table: %w", err)
	}
	return nil
}

func (s *SQLiteTokenStore) Get(ctx context.Context, userID, provider string) (Token, error) {
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT token_ciphertext FROM oauth_user_tokens WHERE user_id = ? AND provider = ?
	`, userID, provider).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, fmt.Errorf("token not found for user %q provider %q", userID, provider)
	}
	if err != nil {
		return Token{}, fmt.Errorf("query oauth token: %w", err)
	}
	plain, err := gcmDecrypt(s.key, ciphertext)
	if err != nil {
		return Token{}, fmt.Errorf("decrypt oauth token: %w", err)
	}
	var token Token
	if err := json.Unmarshal(plain, &token); err != nil {
		return Token{}, fmt.Errorf("decode oauth token: %w", err)
	}
	return token, nil
}

func (s *SQLiteTokenStore) Set(ctx context.Context, userID, provider string, token Token) error {
	if userID == "" || provider == "" {
		return errors.New("user id and provider are required")
	}
	plain, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode oauth token: %w", err)
	}
	ciphertext, err := gcmEncrypt(s.key, plain)
	if err != nil {
		return fmt.Errorf("encrypt oauth token: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_user_tokens (user_id, provider, token_ciphertext, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, provider) DO UPDATE SET
			token_ciphertext=excluded.token_ciphertext,
			updated_at=excluded.updated_at
	`, userID, provider, ciphertext, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store oauth token: %w", err)
	}
	return nil
}

func (s *SQLiteTokenStore) Delete(ctx context.Context, userID, provider string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_user_tokens WHERE user_id = ? AND provider = ?`, userID, provider)
	if err != nil {
		return fmt.Errorf("delete oauth token: %w", err)
	}
	return nil
}

func (s *SQLiteTokenStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func deriveKey(key string) []byte {
	if decoded, err := base64.StdEncoding.DecodeString(key); err == nil && len(decoded) >= 16 {
		sum := sha256.Sum256(decoded)
		return sum[:]
	}
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

func gcmEncrypt(key []byte, plain []byte) ([]byte, error) {
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
	return append(nonce, gcm.Seal(nil, nonce, plain, nil)...), nil
}

func gcmDecrypt(key []byte, ciphertext []byte) ([]byte, error) {
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
	return gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
}

var _ TokenStore = (*SQLiteTokenStore)(nil)
