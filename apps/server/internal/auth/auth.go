package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const sessionLifetime = 7 * 24 * time.Hour

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type Session struct {
	ID        string
	User      User
	ExpiresAt time.Time
}

type Service struct {
	db *sql.DB
}

func New(db *sql.DB) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("auth database is required")
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin auth schema: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS auth_state (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			initialized INTEGER NOT NULL CHECK (initialized IN (0, 1)),
			initialized_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS auth_users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			username_key TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (
			digest BLOB PRIMARY KEY CHECK (length(digest) = 32),
			user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			revoked_at INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS auth_sessions_user_id ON auth_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS auth_sessions_expiry ON auth_sessions(expires_at)`,
		`INSERT INTO auth_state(singleton, initialized) VALUES (1, 0) ON CONFLICT(singleton) DO NOTHING`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize auth schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit auth schema: %w", err)
	}
	return &Service{db: db}, nil
}

func (service *Service) NeedsSetup(ctx context.Context) (bool, error) {
	var initialized int
	if err := service.db.QueryRowContext(ctx, `SELECT initialized FROM auth_state WHERE singleton = 1`).Scan(&initialized); err != nil {
		return false, fmt.Errorf("read administrator setup state: %w", err)
	}
	return initialized == 0, nil
}

func (service *Service) Setup(ctx context.Context, username, password string) (Session, error) {
	needsSetup, err := service.NeedsSetup(ctx)
	if err != nil {
		return Session{}, err
	}
	if !needsSetup {
		return Session{}, setupComplete()
	}
	displayName, usernameKey, err := normalizeUsername(username)
	if err != nil {
		return Session{}, err
	}
	if err := validateNewPassword(password); err != nil {
		return Session{}, err
	}
	userID, err := randomID(16)
	if err != nil {
		return Session{}, fmt.Errorf("generate user ID: %w", err)
	}
	sessionID, digest, err := newSessionCredential()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(sessionLifetime)

	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin administrator setup: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE auth_state SET initialized = 1, initialized_at = ? WHERE singleton = 1 AND initialized = 0`, now.Unix())
	if err != nil {
		return Session{}, fmt.Errorf("claim administrator setup: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("inspect administrator setup claim: %w", err)
	}
	if changed != 1 {
		return Session{}, setupComplete()
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return Session{}, err
	}
	if err := context.Cause(ctx); err != nil {
		return Session{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_users(id, username, username_key, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, displayName, usernameKey, passwordHash, now.Unix(), now.Unix()); err != nil {
		return Session{}, fmt.Errorf("create administrator: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_sessions(digest, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		digest, userID, now.Unix(), expiresAt.Unix()); err != nil {
		return Session{}, fmt.Errorf("create setup session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit administrator setup: %w", err)
	}
	return Session{ID: sessionID, User: User{ID: userID, Username: displayName}, ExpiresAt: expiresAt}, nil
}

func (service *Service) Login(ctx context.Context, username, password string) (Session, error) {
	_, usernameKey, err := normalizeUsername(username)
	if err != nil || len(password) > passwordMaximumBytes {
		if len(password) <= passwordMaximumBytes {
			burnInvalidPassword(password)
		}
		return Session{}, invalidCredentials()
	}
	var user User
	var passwordHash string
	err = service.db.QueryRowContext(ctx, `SELECT id, username, password_hash FROM auth_users WHERE username_key = ?`, usernameKey).
		Scan(&user.ID, &user.Username, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		burnInvalidPassword(password)
		return Session{}, invalidCredentials()
	}
	if err != nil {
		return Session{}, fmt.Errorf("read login account: %w", err)
	}
	matches, err := passwordMatches(passwordHash, password)
	if err != nil {
		return Session{}, fmt.Errorf("verify stored password: %w", err)
	}
	if !matches {
		return Session{}, invalidCredentials()
	}
	if err := context.Cause(ctx); err != nil {
		return Session{}, err
	}
	sessionID, digest, err := newSessionCredential()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(sessionLifetime)
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin login session: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO auth_sessions(digest, user_id, created_at, expires_at)
		SELECT ?, id, ?, ? FROM auth_users WHERE id = ? AND password_hash = ?`,
		digest, now.Unix(), expiresAt.Unix(), user.ID, passwordHash)
	if err != nil {
		return Session{}, fmt.Errorf("create login session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("inspect login session: %w", err)
	}
	if changed != 1 {
		return Session{}, invalidCredentials()
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit login session: %w", err)
	}
	return Session{ID: sessionID, User: user, ExpiresAt: expiresAt}, nil
}

func (service *Service) Validate(ctx context.Context, sessionID string) (User, error) {
	digest, ok := sessionDigest(sessionID)
	if !ok {
		return User{}, authRequired()
	}
	var user User
	now := time.Now().UTC().Unix()
	err := service.db.QueryRowContext(ctx, `SELECT users.id, users.username
		FROM auth_sessions AS sessions
		JOIN auth_users AS users ON users.id = sessions.user_id
		WHERE sessions.digest = ? AND sessions.revoked_at IS NULL AND sessions.expires_at > ?`, digest, now).
		Scan(&user.ID, &user.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, authRequired()
	}
	if err != nil {
		return User{}, fmt.Errorf("validate session: %w", err)
	}
	return user, nil
}

func (service *Service) Logout(ctx context.Context, sessionID string) error {
	digest, ok := sessionDigest(sessionID)
	if !ok {
		return authRequired()
	}
	now := time.Now().UTC().Unix()
	result, err := service.db.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = ?
		WHERE digest = ? AND revoked_at IS NULL AND expires_at > ?`, now, digest, now)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect session revocation: %w", err)
	}
	if changed != 1 {
		return authRequired()
	}
	return nil
}

func (service *Service) ChangePassword(ctx context.Context, sessionID, currentPassword, newPassword string) error {
	digest, ok := sessionDigest(sessionID)
	if !ok {
		return authRequired()
	}
	now := time.Now().UTC().Unix()
	var userID, oldHash string
	err := service.db.QueryRowContext(ctx, `SELECT users.id, users.password_hash
		FROM auth_sessions AS sessions
		JOIN auth_users AS users ON users.id = sessions.user_id
		WHERE sessions.digest = ? AND sessions.revoked_at IS NULL AND sessions.expires_at > ?`, digest, now).
		Scan(&userID, &oldHash)
	if errors.Is(err, sql.ErrNoRows) {
		return authRequired()
	}
	if err != nil {
		return fmt.Errorf("read password-change account: %w", err)
	}
	if err := validateNewPassword(newPassword); err != nil {
		return err
	}
	if len(currentPassword) > passwordMaximumBytes {
		return invalidCredentials()
	}
	matches, err := passwordMatches(oldHash, currentPassword)
	if err != nil {
		return fmt.Errorf("verify stored password: %w", err)
	}
	if !matches {
		return invalidCredentials()
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	now = time.Now().UTC().Unix()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE auth_users SET password_hash = ?, updated_at = ?
		WHERE id = ? AND password_hash = ? AND EXISTS (
			SELECT 1 FROM auth_sessions WHERE digest = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ?
		)`, newHash, now, userID, oldHash, digest, userID, now)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect password change: %w", err)
	}
	if changed != 1 {
		return authRequired()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, userID); err != nil {
		return fmt.Errorf("revoke sessions after password change: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}

func (service *Service) ResetPassword(ctx context.Context, username, newPassword string) error {
	_, usernameKey, err := normalizeUsername(username)
	if err != nil {
		return err
	}
	if err := validateNewPassword(newPassword); err != nil {
		return err
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer tx.Rollback()
	var userID string
	if err := tx.QueryRowContext(ctx, `UPDATE auth_users SET password_hash = ?, updated_at = ? WHERE username_key = ? RETURNING id`,
		newHash, now, usernameKey).Scan(&userID); errors.Is(err, sql.ErrNoRows) {
		return invalidCredentials()
	} else if err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, userID); err != nil {
		return fmt.Errorf("revoke sessions after password reset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

func normalizeUsername(username string) (string, string, error) {
	if !utf8.ValidString(username) {
		return "", "", invalidRequest("username must contain valid text")
	}
	displayName := norm.NFKC.String(strings.TrimSpace(username))
	if !utf8.ValidString(displayName) || displayName == "" || utf8.RuneCountInString(displayName) > 64 || len(displayName) > 256 {
		return "", "", invalidRequest("username must contain 1-64 characters")
	}
	for _, character := range displayName {
		if unicode.IsControl(character) {
			return "", "", invalidRequest("username must not contain control characters")
		}
	}
	usernameKey := cases.Fold().String(displayName)
	return displayName, usernameKey, nil
}

func randomID(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func newSessionCredential() (string, []byte, error) {
	sessionID, err := randomID(32)
	if err != nil {
		return "", nil, fmt.Errorf("generate session credential: %w", err)
	}
	digest := sha256.Sum256([]byte(sessionID))
	return sessionID, digest[:], nil
}

func sessionDigest(sessionID string) ([]byte, bool) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(sessionID)
	if err != nil || len(decoded) != 32 {
		return nil, false
	}
	digest := sha256.Sum256([]byte(sessionID))
	return digest[:], true
}

func invalidRequest(message string) error {
	return fault.New(http.StatusBadRequest, "INVALID_REQUEST", message)
}

func authRequired() error {
	return fault.New(http.StatusUnauthorized, "AUTH_REQUIRED", "authentication is required")
}

func invalidCredentials() error {
	return fault.New(http.StatusUnauthorized, "INVALID_CREDENTIALS", "username or password is incorrect")
}

func setupComplete() error {
	return fault.New(http.StatusConflict, "SETUP_COMPLETE", "administrator setup is already complete")
}
