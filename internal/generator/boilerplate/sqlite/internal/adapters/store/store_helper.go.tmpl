package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/mattn/go-sqlite3"
	"github.com/samber/oops"
)

func (s *Store) WithinTransaction(ctx context.Context, tFunc func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return oops.Code("db_begin_failed").Wrap(err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = tFunc(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return oops.Code("db_commit_failed").Wrap(err)
	}
	return nil
}

func DecorateError(err error, op string) error {
	if err == nil {
		return nil
	}
	o := oops.Code("database_error").With("op", op)
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return o.
			With("sqlite_code", sqliteErr.Code).
			With("sqlite_extended_code", sqliteErr.ExtendedCode).
			Wrap(err)
	}
	return o.Wrap(err)
}

func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique ||
			sqliteErr.Code == sqlite3.ErrConstraint
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func collectRows[T any](rows *sql.Rows, op string, scan func(*sql.Rows) (T, error)) ([]T, error) {
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, DecorateError(err, op)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, DecorateError(err, op)
	}
	return out, nil
}

func queryRow[T any](row *sql.Row, notFound error, op string, scan func(*sql.Row) (T, error)) (T, error) {
	v, err := scan(row)
	if err != nil {
		var zero T
		if errors.Is(err, sql.ErrNoRows) {
			return zero, notFound
		}
		return zero, DecorateError(err, op)
	}
	return v, nil
}
