package playback

import (
	"context"
	"errors"
	"time"
)

var ErrMediaSigningKeyConflict = errors.New("playback: active media signing key already exists")

func (store *Store) ActiveMediaSigningKey(ctx context.Context) (EncryptedMediaSigningKey, bool, error) {
	result := EncryptedMediaSigningKey{}
	found := false
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT key_id,key_digest,key_ciphertext,key_nonce,wrapping_key_id,created_at
			FROM media_signing_keys WHERE retired_at IS NULL`)
		if err != nil {
			return err
		}
		defer stmt.close()
		row, err := stmt.step()
		if err != nil || !row {
			return err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, stmt.text(5))
		if err != nil {
			return err
		}
		ciphertext, ok := stmt.current[2].([]byte)
		if !ok {
			return ErrCorruptDatabase
		}
		nonce, ok := stmt.current[3].([]byte)
		if !ok {
			return ErrCorruptDatabase
		}
		result = EncryptedMediaSigningKey{
			ID: SigningKeyID(stmt.text(0)), Digest: stmt.text(1), Ciphertext: append([]byte(nil), ciphertext...),
			Nonce: append([]byte(nil), nonce...), WrappingKeyID: stmt.text(4), CreatedAt: createdAt,
		}
		found = true
		return nil
	})
	return result, found, err
}

func (store *Store) InsertMediaSigningKey(ctx context.Context, key EncryptedMediaSigningKey) error {
	if key.ID == "" || key.Digest == "" || len(key.Ciphertext) == 0 || len(key.Nonce) == 0 || key.WrappingKeyID == "" || key.CreatedAt.IsZero() {
		return ErrInvalidRequest
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare("SELECT count(*) FROM media_signing_keys WHERE retired_at IS NULL")
		if err != nil {
			return err
		}
		row, err := stmt.step()
		if err != nil {
			stmt.close()
			return err
		}
		count := int64(0)
		if row {
			count = stmt.int64(0)
		}
		stmt.close()
		if count != 0 {
			return ErrMediaSigningKeyConflict
		}
		return execBound(db, `INSERT INTO media_signing_keys(
			key_id,key_digest,key_ciphertext,key_nonce,wrapping_key_id,created_at
		) VALUES (?,?,?,?,?,?)`, func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, string(key.ID)); err != nil {
				return err
			}
			if err := stmt.bindText(2, key.Digest); err != nil {
				return err
			}
			if err := stmt.bind(3, key.Ciphertext); err != nil {
				return err
			}
			if err := stmt.bind(4, key.Nonce); err != nil {
				return err
			}
			if err := stmt.bindText(5, key.WrappingKeyID); err != nil {
				return err
			}
			return stmt.bindText(6, key.CreatedAt.UTC().Format(time.RFC3339Nano))
		})
	})
}
