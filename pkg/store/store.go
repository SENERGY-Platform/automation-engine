/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Dialect string

const (
	Postgres Dialect = "postgres"
	SQLite   Dialect = "sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store persists everything in one relational database, Postgres in the cloud and SQLite
// on gateways and in tests (OPS-02, FOG-03). Both share schema and queries.
type Store struct {
	db      *sql.DB
	dialect Dialect
	// the last write failed because the disk or the page limit was full (LONG-07)
	full atomic.Bool
	// nothing removes rights from permissions-v2, e.g. on a gateway; the queue would only grow
	withoutRemovals atomic.Bool
}

// DisablePermissionRemovals stops queueing rights for removal from permissions-v2, for an
// engine without permissions-v2.
func (this *Store) DisablePermissionRemovals() {
	this.withoutRemovals.Store(true)
}

// Open connects and applies pending migrations (OPS-03).
// For SQLite, dsn is a file path or a modernc.org/sqlite URI; pragmas for foreign keys,
// immediate transactions and incremental vacuum (LONG-06) are added.
func Open(ctx context.Context, dialect Dialect, dsn string) (*Store, error) {
	var db *sql.DB
	var err error
	switch dialect {
	case Postgres:
		db, err = sql.Open("pgx", dsn)
	case SQLite:
		db, err = sql.Open("sqlite", sqliteDsn(dsn))
		if err == nil {
			// one connection serializes all access; SQLite allows only one writer anyway and
			// this avoids SQLITE_BUSY between connections of the same process
			db.SetMaxOpenConns(1)
			db.SetConnMaxIdleTime(0)
			db.SetConnMaxLifetime(0)
		}
	default:
		return nil, fmt.Errorf("unknown database dialect %q", dialect)
	}
	if err != nil {
		return nil, err
	}
	result := &Store{db: db, dialect: dialect}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, model.Unavailable("database not reachable: %v", err)
	}
	if err = result.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return result, nil
}

func sqliteDsn(dsn string) string {
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	// auto_vacuum must be set before the first table exists
	return dsn + sep + "_txlock=immediate&_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=auto_vacuum(incremental)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

func (this *Store) Close() error {
	return this.db.Close()
}

func (this *Store) Dialect() Dialect {
	return this.dialect
}

func (this *Store) migrate(ctx context.Context) error {
	return this.Tx(ctx, func(tx *Tx) error {
		if this.dialect == Postgres {
			// serializes migrations of concurrently starting replicas
			if _, err := tx.exec("SELECT pg_advisory_xact_lock(7302351)"); err != nil {
				return err
			}
		}
		if _, err := tx.exec("CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)"); err != nil {
			return err
		}
		applied := map[int]bool{}
		rows, err := tx.query("SELECT version FROM schema_migrations")
		if err != nil {
			return err
		}
		for rows.Next() {
			var v int
			if err = rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			applied[v] = true
		}
		rows.Close()
		files, err := fs.Glob(migrations, "migrations/*.sql")
		if err != nil {
			return err
		}
		sort.Strings(files)
		for _, file := range files {
			name := strings.TrimPrefix(file, "migrations/")
			version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
			if err != nil {
				return fmt.Errorf("migration %v: %w", name, err)
			}
			if applied[version] {
				continue
			}
			content, err := migrations.ReadFile(file)
			if err != nil {
				return err
			}
			// comments go first, they may contain semicolons
			for _, statement := range strings.Split(this.dialectSpecific(stripComments(string(content))), ";") {
				if strings.TrimSpace(statement) == "" {
					continue
				}
				if _, err = tx.exec(statement); err != nil {
					return fmt.Errorf("migration %v: %w", name, err)
				}
			}
			if _, err = tx.exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", version, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

// dialectSpecific replaces the few placeholders for syntax that differs between the dialects
func (this *Store) dialectSpecific(statements string) string {
	identity := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if this.dialect == Postgres {
		identity = "BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY"
	}
	return strings.ReplaceAll(statements, "{{IDENTITY}}", identity)
}

func stripComments(statement string) string {
	var lines []string
	for _, line := range strings.Split(statement, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// Tx runs fn in a transaction; it is committed if fn returns nil.
func (this *Store) Tx(ctx context.Context, fn func(tx *Tx) error) (err error) {
	sqlTx, err := this.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Unavailable("unable to begin transaction: %v", err)
	}
	tx := &Tx{tx: sqlTx, dialect: this.dialect, ctx: ctx, withoutRemovals: this.withoutRemovals.Load()}
	defer func() {
		if p := recover(); p != nil {
			_ = sqlTx.Rollback()
			panic(p)
		}
	}()
	if err = fn(tx); err != nil {
		_ = sqlTx.Rollback()
		this.noteFull(err)
		return err
	}
	if err = sqlTx.Commit(); err != nil {
		this.noteFull(err)
		return fmt.Errorf("commit: %w", err)
	}
	this.full.Store(false)
	return nil
}

// noteFull remembers a write that failed for lack of space; SQLite has rolled it back, so the
// state stays consistent (LONG-07)
func (this *Store) noteFull(err error) {
	if IsDiskFull(err) {
		this.full.Store(true)
	}
}

// Full reports whether the last write failed for lack of space; a successful write clears it.
func (this *Store) Full() bool {
	return this.full.Load()
}

// IsDiskFull reports a write that failed for lack of space (SQLITE_FULL, Postgres disk_full).
func IsDiskFull(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "53100"
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xff == sqlite3.SQLITE_FULL
	}
	return false
}

// IsUniqueViolation reports whether err is a violated unique or primary key constraint.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// Vacuum returns freed pages to the file system in small steps (LONG-06); a no-op for Postgres.
func (this *Store) Vacuum(ctx context.Context, pages int) error {
	if this.dialect != SQLite {
		return nil
	}
	_, err := this.db.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages))
	return err
}

type Tx struct {
	tx              *sql.Tx
	dialect         Dialect
	ctx             context.Context
	withoutRemovals bool
}

// rebind rewrites ? placeholders to $n for Postgres; queries never contain ? in literals
func (this *Tx) rebind(query string) string {
	if this.dialect != Postgres || !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (this *Tx) exec(query string, args ...any) (sql.Result, error) {
	return this.tx.ExecContext(this.ctx, this.rebind(query), args...)
}

func (this *Tx) query(query string, args ...any) (*sql.Rows, error) {
	return this.tx.QueryContext(this.ctx, this.rebind(query), args...)
}

func (this *Tx) queryRow(query string, args ...any) *sql.Row {
	return this.tx.QueryRowContext(this.ctx, this.rebind(query), args...)
}

// forUpdate locks selected rows until the end of the transaction; SQLite transactions are
// already exclusive because they begin immediately
func (this *Tx) forUpdate(lock bool) string {
	if lock && this.dialect == Postgres {
		return " FOR UPDATE"
	}
	return ""
}

func (this *Tx) count(query string, args ...any) (result int, err error) {
	err = this.queryRow(query, args...).Scan(&result)
	return result, err
}

func millis(t time.Time) int64 {
	return t.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

func nullMillis(t *time.Time) sql.NullInt64 {
	if t == nil || t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

func fromNullMillis(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMillis(v.Int64)
	return &t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func toJson(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		// values are normalized before they reach the store
		panic(fmt.Sprintf("store: value not JSON serializable: %v", err))
	}
	return string(raw)
}

func fromJson[T any](raw string) (result T) {
	if raw == "" {
		return result
	}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func notFound(err error, format string, args ...any) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.NotFound(format, args...)
	}
	return err
}

// Access restricts list queries to what the caller may see: everything for admins,
// otherwise own resources plus explicitly granted ids (filled from permissions-v2 in M2).
type Access struct {
	Admin bool
	Owner string
	Ids   []string
}

// where builds a condition with a list of conditions joined by AND
type where struct {
	conditions []string
	args       []any
}

// network filters by the gateway: "cloud" for resources of the cloud, a network id for those of
// a gateway, empty for all (API-05)
func (this *where) network(networkId string) {
	switch networkId {
	case "":
	case "cloud":
		this.add("network_id = ''")
	default:
		this.add("network_id = ?", networkId)
	}
}

func (this *where) add(condition string, args ...any) {
	this.conditions = append(this.conditions, condition)
	this.args = append(this.args, args...)
}

func (this *where) in(column string, values []string) {
	if len(values) == 0 {
		this.add("1 = 0")
		return
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", ")
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	this.add(column+" IN ("+placeholders+")", args...)
}

func (this *where) access(prefix string, access Access) {
	if access.Admin {
		return
	}
	if len(access.Ids) == 0 {
		this.add(prefix+"owner = ?", access.Owner)
		return
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(access.Ids)), ", ")
	args := []any{access.Owner}
	for _, id := range access.Ids {
		args = append(args, id)
	}
	this.add("("+prefix+"owner = ? OR "+prefix+"id IN ("+placeholders+"))", args...)
}

// search finds the term in any of the columns, ignoring case
func (this *where) search(term string, columns ...string) {
	if term == "" {
		return
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(term))
	conditions := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	for _, column := range columns {
		conditions = append(conditions, "LOWER("+column+") LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escaped+"%")
	}
	this.add("("+strings.Join(conditions, " OR ")+")", args...)
}

func (this *where) String() string {
	if len(this.conditions) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(this.conditions, " AND ")
}

// orderBy translates "field.asc" or "field.desc" through an allow list of columns
func orderBy(sort string, columns map[string]string, fallback string) (string, error) {
	if sort == "" {
		return " ORDER BY " + fallback, nil
	}
	field, direction, _ := strings.Cut(sort, ".")
	column, ok := columns[field]
	if !ok {
		return "", model.BadRequest("unknown sort field %q", field)
	}
	switch strings.ToLower(direction) {
	case "", "asc":
		direction = "ASC"
	case "desc":
		direction = "DESC"
	default:
		return "", model.BadRequest("unknown sort direction %q", direction)
	}
	// the id makes the order stable for paging
	return " ORDER BY " + column + " " + direction + ", id ASC", nil
}

func limitOffset(page model.PageOptions) (string, []any) {
	page = page.Normalized()
	return " LIMIT ? OFFSET ?", []any{page.Limit, page.Offset}
}

// Ping checks that the database is reachable (readiness, OPS-04).
func (this *Store) Ping(ctx context.Context) error {
	if this.Full() {
		return model.Unavailable("the storage is full, execution is paused until space is free")
	}
	return this.db.PingContext(ctx)
}
