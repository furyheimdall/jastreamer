package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/auth"
	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/fault"
)

func TestSetupIsAtomicAndNeverReopens(t *testing.T) {
	service, db := openService(t)
	ctx := context.Background()

	const contenders = 8
	start := make(chan struct{})
	results := make(chan error, contenders)
	var group sync.WaitGroup
	for range contenders {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := service.Setup(ctx, "Administrator", "a secure password")
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		requireFaultCode(t, err, "SETUP_COMPLETE")
		conflicts++
	}
	if successes != 1 || conflicts != contenders-1 {
		t.Fatalf("setup outcomes: successes=%d conflicts=%d", successes, conflicts)
	}
	needsSetup, err := service.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("read setup state: %v", err)
	}
	if needsSetup {
		t.Fatal("setup remained open after administrator creation")
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM auth_users`); err != nil {
		t.Fatalf("simulate lost administrator row: %v", err)
	}
	needsSetup, err = service.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("read durable setup state: %v", err)
	}
	if needsSetup {
		t.Fatal("deleting users reopened administrator setup")
	}
	_, err = service.Setup(ctx, "Replacement", "another secure password")
	requireFaultCode(t, err, "SETUP_COMPLETE")
}

func TestPasswordTransitionsRevokeEveryPriorSession(t *testing.T) {
	service, db := openService(t)
	ctx := context.Background()
	setup, err := service.Setup(ctx, "Administrator", "first secure password")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	second, err := service.Login(ctx, " administrator ", "first secure password")
	if err != nil {
		t.Fatalf("normalized login: %v", err)
	}
	if second.User.Username != "Administrator" {
		t.Fatalf("stored username = %q", second.User.Username)
	}

	err = service.ChangePassword(ctx, setup.ID, "wrong current password", "second secure password")
	requireFaultCode(t, err, "INVALID_CREDENTIALS")
	if _, err := service.Validate(ctx, setup.ID); err != nil {
		t.Fatalf("wrong current password revoked session: %v", err)
	}

	if err := service.ChangePassword(ctx, setup.ID, "first secure password", "second secure password"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	requireFaultCode(t, validateError(ctx, service, setup.ID), "AUTH_REQUIRED")
	requireFaultCode(t, validateError(ctx, service, second.ID), "AUTH_REQUIRED")
	_, err = service.Login(ctx, "Administrator", "first secure password")
	requireFaultCode(t, err, "INVALID_CREDENTIALS")

	current, err := service.Login(ctx, "ADMINISTRATOR", "second secure password")
	if err != nil {
		t.Fatalf("login with changed password: %v", err)
	}
	if err := service.Logout(ctx, current.ID); err != nil {
		t.Fatalf("logout: %v", err)
	}
	requireFaultCode(t, validateError(ctx, service, current.ID), "AUTH_REQUIRED")
	requireFaultCode(t, service.Logout(ctx, current.ID), "AUTH_REQUIRED")

	beforeReset, err := service.Login(ctx, "Administrator", "second secure password")
	if err != nil {
		t.Fatalf("login before reset: %v", err)
	}
	if err := service.ResetPassword(ctx, "administrator", "third secure password"); err != nil {
		t.Fatalf("reset password: %v", err)
	}
	requireFaultCode(t, validateError(ctx, service, beforeReset.ID), "AUTH_REQUIRED")
	_, err = service.Login(ctx, "Administrator", "second secure password")
	requireFaultCode(t, err, "INVALID_CREDENTIALS")

	afterReset, err := service.Login(ctx, "Administrator", "third secure password")
	if err != nil {
		t.Fatalf("login after reset: %v", err)
	}
	if _, err := service.Validate(ctx, afterReset.ID); err != nil {
		t.Fatalf("validate reset session: %v", err)
	}
	digest := sha256.Sum256([]byte(afterReset.ID))
	if _, err := db.ExecContext(ctx, `UPDATE auth_sessions SET expires_at = ? WHERE digest = ?`, time.Now().Add(-time.Minute).Unix(), digest[:]); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	requireFaultCode(t, validateError(ctx, service, afterReset.ID), "AUTH_REQUIRED")
}

func TestSetupAndSessionPersistWithoutPlaintextCredentials(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	firstDB, err := database.Open(path)
	if err != nil {
		t.Fatalf("open first database: %v", err)
	}
	first, err := auth.New(firstDB)
	if err != nil {
		_ = firstDB.Close()
		t.Fatalf("open first auth service: %v", err)
	}
	session, err := first.Setup(ctx, "Administrator", "persistent secure password")
	if err != nil {
		_ = firstDB.Close()
		t.Fatalf("setup: %v", err)
	}
	var storedPassword string
	var storedDigest []byte
	if err := firstDB.QueryRowContext(ctx, `SELECT password_hash FROM auth_users WHERE id = ?`, session.User.ID).Scan(&storedPassword); err != nil {
		_ = firstDB.Close()
		t.Fatalf("read stored password: %v", err)
	}
	if err := firstDB.QueryRowContext(ctx, `SELECT digest FROM auth_sessions WHERE user_id = ?`, session.User.ID).Scan(&storedDigest); err != nil {
		_ = firstDB.Close()
		t.Fatalf("read stored session: %v", err)
	}
	if storedPassword == "persistent secure password" {
		_ = firstDB.Close()
		t.Fatal("database stored a plaintext password")
	}
	expectedDigest := sha256.Sum256([]byte(session.ID))
	if !bytes.Equal(storedDigest, expectedDigest[:]) {
		_ = firstDB.Close()
		t.Fatal("database did not store the session digest")
	}
	if err := firstDB.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	secondDB, err := database.Open(path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() {
		if err := secondDB.Close(); err != nil {
			t.Errorf("close second database: %v", err)
		}
	})
	second, err := auth.New(secondDB)
	if err != nil {
		t.Fatalf("reopen auth service: %v", err)
	}
	needsSetup, err := second.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("read setup state after reopen: %v", err)
	}
	if needsSetup {
		t.Fatal("administrator setup reopened after restart")
	}
	user, err := second.Validate(ctx, session.ID)
	if err != nil {
		t.Fatalf("validate persisted session: %v", err)
	}
	if user != session.User {
		t.Fatalf("persisted session user = %#v, want %#v", user, session.User)
	}
}

func openService(t *testing.T) (*auth.Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	service, err := auth.New(db)
	if err != nil {
		t.Fatalf("open auth service: %v", err)
	}
	return service, db
}

func validateError(ctx context.Context, service *auth.Service, sessionID string) error {
	_, err := service.Validate(ctx, sessionID)
	return err
}

func requireFaultCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", code)
	}
	var value *fault.Error
	if !errors.As(err, &value) {
		t.Fatalf("expected fault.Error, got %T: %v", err, err)
	}
	if value.Code != code {
		t.Fatalf("error code = %q, want %q", value.Code, code)
	}
}
