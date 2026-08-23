package playback

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	modernsqlite "modernc.org/sqlite"
)

type sqliteDB struct {
	connection driver.Conn
	pending    error
}

type sqliteStmt struct {
	database  *sqliteDB
	statement driver.Stmt
	arguments []driver.Value
	rows      driver.Rows
	current   []driver.Value
	done      bool
}

type onlineBackuper interface {
	NewBackup(string) (*modernsqlite.Backup, error)
}

func openSQLite(path string) (*sqliteDB, error) {
	connection, err := (&modernsqlite.Driver{}).Open(path)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	return &sqliteDB{connection: connection}, nil
}

func (db *sqliteDB) close() error {
	if err := errors.Join(db.takePending(), db.connection.Close()); err != nil {
		return fmt.Errorf("sqlite close: %w", err)
	}
	return nil
}

func (db *sqliteDB) exec(query string) error {
	if err := db.takePending(); err != nil {
		return err
	}
	executor, ok := db.connection.(driver.ExecerContext)
	if !ok {
		return errors.New("sqlite connection does not support direct execution")
	}
	if _, err := executor.ExecContext(context.Background(), query, nil); err != nil {
		return fmt.Errorf("sqlite exec: %w", err)
	}
	return nil
}

func (db *sqliteDB) prepare(query string) (*sqliteStmt, error) {
	if err := db.takePending(); err != nil {
		return nil, err
	}
	statement, err := db.connection.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("sqlite prepare: %w", err)
	}
	count := statement.NumInput()
	count = max(count, 0)
	return &sqliteStmt{database: db, statement: statement, arguments: make([]driver.Value, count)}, nil
}

func (stmt *sqliteStmt) close() {
	var rowsErr error
	if stmt.rows != nil {
		rowsErr = stmt.rows.Close()
	}
	stmt.database.pending = errors.Join(stmt.database.pending, rowsErr, stmt.statement.Close())
}

func (db *sqliteDB) takePending() error {
	err := db.pending
	db.pending = nil
	return err
}

func (stmt *sqliteStmt) bindText(index int, value string) error {
	return stmt.bind(index, value)
}

func (stmt *sqliteStmt) bindInt64(index int, value int64) error {
	return stmt.bind(index, value)
}

func (stmt *sqliteStmt) bind(index int, value driver.Value) error {
	if index <= 0 {
		return fmt.Errorf("sqlite bind index %d out of range", index)
	}
	for len(stmt.arguments) < index {
		stmt.arguments = append(stmt.arguments, nil)
	}
	stmt.arguments[index-1] = value
	return nil
}

func (stmt *sqliteStmt) step() (bool, error) {
	if stmt.done {
		return false, nil
	}
	if stmt.rows == nil {
		query, ok := stmt.statement.(driver.StmtQueryContext)
		if !ok {
			return false, errors.New("sqlite statement does not support contextual query")
		}
		arguments := make([]driver.NamedValue, len(stmt.arguments))
		for index, value := range stmt.arguments {
			arguments[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
		}
		rows, err := query.QueryContext(context.Background(), arguments)
		if err != nil {
			return false, fmt.Errorf("sqlite step: %w", err)
		}
		stmt.rows = rows
		stmt.current = make([]driver.Value, len(rows.Columns()))
	}
	if err := stmt.rows.Next(stmt.current); err != nil {
		if errors.Is(err, io.EOF) {
			stmt.done = true
			closeErr := stmt.rows.Close()
			stmt.rows = nil
			return false, closeErr
		}
		return false, fmt.Errorf("sqlite row: %w", err)
	}
	return true, nil
}

func (stmt *sqliteStmt) reset() error {
	var err error
	if stmt.rows != nil {
		err = stmt.rows.Close()
	}
	stmt.rows = nil
	stmt.current = nil
	stmt.done = false
	clear(stmt.arguments)
	return err
}

func (stmt *sqliteStmt) text(column int) string {
	switch value := stmt.current[column].(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func (stmt *sqliteStmt) int64(column int) int64 {
	switch value := stmt.current[column].(type) {
	case int64:
		return value
	default:
		return 0
	}
}

func (stmt *sqliteStmt) isNull(column int) bool {
	return stmt.current[column] == nil
}

func (db *sqliteDB) versionNumber() (int, error) {
	stmt, err := db.prepare("SELECT sqlite_version()")
	if err != nil {
		return 0, err
	}
	defer stmt.close()
	row, err := stmt.step()
	if err != nil || !row {
		return 0, errors.Join(err, errors.New("sqlite version query returned no row"))
	}
	parts := strings.Split(stmt.text(0), ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid SQLite version %q", stmt.text(0))
	}
	numbers := [3]int{}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			return 0, fmt.Errorf("invalid SQLite version %q: %w", stmt.text(0), err)
		}
		numbers[index] = number
	}
	return numbers[0]*1_000_000 + numbers[1]*1_000 + numbers[2], nil
}

func sqliteBackup(source *sqliteDB, destinationPath string) (err error) {
	if err := source.takePending(); err != nil {
		return err
	}
	backuper, ok := source.connection.(onlineBackuper)
	if !ok {
		return errors.New("sqlite connection does not support online backup")
	}
	backup, err := backuper.NewBackup(destinationPath)
	if err != nil {
		return fmt.Errorf("sqlite backup initialize: %w", err)
	}
	defer func() { err = errors.Join(err, backup.Finish()) }()
	if _, err := backup.Step(-1); err != nil {
		return fmt.Errorf("sqlite backup step: %w", err)
	}
	return nil
}

func execBound(db *sqliteDB, query string, bind func(*sqliteStmt) error) error {
	stmt, err := db.prepare(query)
	if err != nil {
		return err
	}
	defer stmt.close()
	if err := bind(stmt); err != nil {
		return err
	}
	if row, err := stmt.step(); err != nil {
		return err
	} else if row {
		return errors.New("sqlite exec unexpectedly returned a row")
	}
	return nil
}
