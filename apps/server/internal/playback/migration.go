package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (run migrationRun) apply(ctx context.Context) error {
	db, config, hook := run.db, run.config, run.hook
	version, err := schemaVersion(db)
	if err != nil {
		return err
	}
	if version > config.SupportedSchema {
		minimumReader, err := minimumReaderVersion(db)
		if err != nil {
			return err
		}
		if minimumReader > config.SupportedSchema {
			return fmt.Errorf(
				"schema version %d requires reader %d, supported %d: %w",
				version, minimumReader, config.SupportedSchema, ErrSchemaTooNew,
			)
		}
		return integrityCheck(db)
	}
	if version == CurrentSchemaVersion {
		return integrityCheck(db)
	}
	if version != 0 && version != 2 && version != 3 && version != 4 && version != 5 && version != 6 {
		return fmt.Errorf("unsupported migration from schema %d", version)
	}
	if err := context.Cause(ctx); err != nil {
		return fmt.Errorf("before migration: %w", err)
	}
	if err := integrityCheck(db); err != nil {
		return err
	}
	if err := os.MkdirAll(config.BackupDirectory, 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	backupPath, err := nextBackupPath(config.BackupDirectory, version)
	if err != nil {
		return err
	}
	if err := sqliteBackup(db, backupPath); err != nil {
		return fmt.Errorf("backup before schema %d: %w", CurrentSchemaVersion, err)
	}
	backupDB, err := openSQLite(backupPath)
	if err != nil {
		return fmt.Errorf("open migration backup: %w", err)
	}
	backupErr := integrityCheck(backupDB)
	closeErr := backupDB.close()
	if err := errors.Join(backupErr, closeErr); err != nil {
		return fmt.Errorf("verify migration backup: %w", err)
	}
	if run.cutpoint != nil {
		if err := run.cutpoint(migrationAfterBackup); err != nil {
			return fmt.Errorf("migration interrupted after backup: %w", err)
		}
	}
	statements, err := migrationStatements(config, version)
	if err != nil {
		return err
	}
	if err := db.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	for _, statement := range statements {
		if err := db.exec(statement); err != nil {
			if rollbackErr := db.exec("ROLLBACK"); rollbackErr != nil {
				return errors.Join(err, rollbackErr)
			}
			return err
		}
	}
	if hook != nil {
		if err := hook(CurrentSchemaVersion); err != nil {
			return errors.Join(
				fmt.Errorf("migration interrupted after expand: %w", err),
				db.exec("ROLLBACK"),
			)
		}
	}
	if run.cutpoint != nil {
		if err := run.cutpoint(migrationAfterExpand); err != nil {
			return errors.Join(fmt.Errorf("migration interrupted after expand: %w", err), db.exec("ROLLBACK"))
		}
		if err := run.cutpoint(migrationBeforeCommit); err != nil {
			return errors.Join(fmt.Errorf("migration interrupted before commit: %w", err), db.exec("ROLLBACK"))
		}
	}
	if err := context.Cause(ctx); err != nil {
		return errors.Join(fmt.Errorf("migration interrupted before commit: %w", err), db.exec("ROLLBACK"))
	}
	if err := db.exec("COMMIT"); err != nil {
		return errors.Join(err, db.exec("ROLLBACK"))
	}
	return integrityCheck(db)
}

func minimumReaderVersion(db *sqliteDB) (int, error) {
	stmt, err := db.prepare("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='playback_schema'")
	if err != nil {
		return 0, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		stmt.close()
		return 0, errors.Join(err, ErrCorruptDatabase)
	}
	exists := stmt.int64(0) == 1
	stmt.close()
	if !exists {
		return 0, nil
	}
	stmt, err = db.prepare("SELECT version,minimum_reader_version FROM playback_schema WHERE singleton=1")
	if err != nil {
		return 0, err
	}
	defer stmt.close()
	row, err = stmt.step()
	if err != nil || !row {
		return 0, errors.Join(err, ErrCorruptDatabase)
	}
	declared, minimum := int(stmt.int64(0)), int(stmt.int64(1))
	version, err := schemaVersion(db)
	if err != nil {
		return 0, err
	}
	if declared != version {
		return 0, ErrCorruptDatabase
	}
	return minimum, nil
}

func schemaVersion(db *sqliteDB) (int, error) {
	stmt, err := db.prepare("PRAGMA user_version")
	if err != nil {
		return 0, err
	}
	defer stmt.close()
	row, err := stmt.step()
	if err != nil {
		return 0, err
	}
	if !row {
		return 0, fmt.Errorf("schema version query returned no row")
	}
	return int(stmt.int64(0)), nil
}

func integrityCheck(db *sqliteDB) error {
	stmt, err := db.prepare("PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCorruptDatabase, err)
	}
	defer stmt.close()
	row, err := stmt.step()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCorruptDatabase, err)
	}
	if !row || stmt.text(0) != "ok" {
		return ErrCorruptDatabase
	}
	return nil
}

func nextBackupPath(directory string, version int) (string, error) {
	for sequence := 1; ; sequence++ {
		path := filepath.Join(directory, fmt.Sprintf("playback-schema-%d-%04d.sqlite", version, sequence))
		_, err := os.Stat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return path, nil
		case err != nil:
			return "", fmt.Errorf("inspect backup path: %w", err)
		}
	}
}
