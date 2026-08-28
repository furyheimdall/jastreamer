package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func RestoreBackup(ctx context.Context, request RestoreRequest) (err error) {
	backupPath, targetPath := request.BackupPath, request.TargetPath
	if !isLocalPath(backupPath) || !isLocalPath(targetPath) {
		return ErrNonLocalDatabase
	}
	if err := context.Cause(ctx); err != nil {
		return fmt.Errorf("before restore: %w", err)
	}
	backup, err := openSQLite(backupPath)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer func() { err = errors.Join(err, backup.close()) }()
	if err := integrityCheck(backup); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	version, err := schemaVersion(backup)
	if err != nil {
		return err
	}
	if version > request.SupportedSchema {
		return ErrSchemaTooNew
	}
	targetDirectory := filepath.Dir(targetPath)
	temporary, err := os.CreateTemp(targetDirectory, "."+filepath.Base(targetPath)+".restore-*")
	if err != nil {
		return fmt.Errorf("create restore target: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close restore target: %w", err)
	}
	defer func() {
		removeErr := os.Remove(temporaryPath)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temporary restore: %w", removeErr))
		}
	}()
	if err := sqliteBackup(backup, temporaryPath); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	restored, err := openSQLite(temporaryPath)
	if err != nil {
		return fmt.Errorf("open restored database: %w", err)
	}
	checkErr := integrityCheck(restored)
	closeErr := restored.close()
	if err := errors.Join(checkErr, closeErr); err != nil {
		return fmt.Errorf("verify restored database: %w", err)
	}
	if err := atomicReplace(temporaryPath, targetPath); err != nil {
		return fmt.Errorf("replace restored database: %w", err)
	}
	return nil
}
