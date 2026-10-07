package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"herdr-space/internal/model"
	_ "modernc.org/sqlite"
)

var (
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("conflict")
	ErrNotFound = errors.New("not found")
)

type Store struct {
	DB   *sql.DB
	Path string
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	dirInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if !dirInfo.IsDir() {
		return nil, ErrInvalid
	}
	if dirInfo.Mode().Perm()&0077 != 0 {
		dir := filepath.Clean(filepath.Dir(path))
		home, _ := os.UserHomeDir()
		if dir == "/" || dir == "." || dir == home || dir == "/tmp" {
			return nil, ErrInvalid
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return nil, err
		}
	}
	existed := false
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, ErrInvalid
		}
		existed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Chmod(path, 0600); err != nil {
			return nil, err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		f, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if createErr != nil {
			return nil, createErr
		}
		if closeErr := f.Close(); closeErr != nil {
			return nil, closeErr
		}
	} else {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{DB: db, Path: path}
	if _, err = db.Exec("PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 2 {
		db.Close()
		return nil, fmt.Errorf("unsupported schema version %d", version)
	}
	if version == 0 {
		if existed {
			if e := s.Backup(path + ".pre-upgrade-" + ID()); e != nil {
				db.Close()
				return nil, e
			}
		}
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, err
		}
		for _, stmt := range []string{
			`CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,path TEXT NOT NULL,created_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY,title TEXT NOT NULL,description TEXT NOT NULL,project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,session_id TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('todo','doing','done')),due_date TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS notes(id TEXT PRIMARY KEY,title TEXT NOT NULL,body TEXT NOT NULL,project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS preferences(id INTEGER PRIMARY KEY CHECK(id=1),theme TEXT NOT NULL CHECK(theme IN ('system','dark','light')))`,
			`INSERT OR IGNORE INTO preferences(id,theme) VALUES(1,'system')`,
			`CREATE TABLE IF NOT EXISTS admin(id INTEGER PRIMARY KEY CHECK(id=1),username TEXT NOT NULL,password_hash TEXT NOT NULL,totp_nonce BLOB NOT NULL,totp_cipher BLOB NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS recovery(code_hash BLOB PRIMARY KEY,used_at TEXT)`,
			`CREATE TABLE IF NOT EXISTS sessions(token_hash BLOB PRIMARY KEY,csrf_hash BLOB NOT NULL,created_at TEXT NOT NULL,seen_at TEXT NOT NULL,expires_at TEXT NOT NULL,revoked_at TEXT)`,
			`CREATE TABLE IF NOT EXISTS totp_steps(step INTEGER PRIMARY KEY,used_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS audit(id TEXT PRIMARY KEY,event TEXT NOT NULL,created_at TEXT NOT NULL)`,
			`CREATE TABLE runtime_references(session_id TEXT PRIMARY KEY,session_json TEXT NOT NULL,socket TEXT NOT NULL,agent_ref TEXT NOT NULL)`,
			`PRAGMA user_version=2`,
		} {
			if _, err = tx.Exec(stmt); err != nil {
				tx.Rollback()
				db.Close()
				return nil, err
			}
		}
		if err = tx.Commit(); err != nil {
			db.Close()
			return nil, err
		}
	}
	if version == 1 {
		if version == 1 && existed {
			if e := s.Backup(path + ".pre-upgrade-" + ID()); e != nil {
				db.Close()
				return nil, e
			}
		}
		tx, e := db.Begin()
		if e != nil {
			db.Close()
			return nil, e
		}
		if _, e = tx.Exec(`CREATE TABLE runtime_references(session_id TEXT PRIMARY KEY,session_json TEXT NOT NULL,socket TEXT NOT NULL,agent_ref TEXT NOT NULL)`); e != nil {
			tx.Rollback()
			db.Close()
			return nil, e
		}
		if _, e = tx.Exec(`PRAGMA user_version=2`); e != nil {
			tx.Rollback()
			db.Close()
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			db.Close()
			return nil, e
		}
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Backup(dest string) error {
	if dest == s.Path {
		return ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	dirInfo, err := os.Lstat(filepath.Dir(dest))
	if err != nil {
		return err
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0077 != 0 {
		return ErrInvalid
	}
	quoted := "'" + strings.ReplaceAll(dest, "'", "''") + "'"
	if _, err := s.DB.Exec("VACUUM INTO " + quoted); err != nil {
		return err
	}
	return os.Chmod(dest, 0600)
}
func (s *Store) Integrity(ctx context.Context) error {
	var v string
	if err := s.DB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&v); err != nil {
		return err
	}
	if v != "ok" {
		return errors.New("database integrity failure")
	}
	return nil
}
func valid(s string, max int, required bool) bool {
	return len(s) <= max && (!required || strings.TrimSpace(s) != "")
}
func fk(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "constraint") {
		return ErrInvalid
	}
	return err
}
func (s *Store) ListProjects(ctx context.Context) ([]model.Project, error) {
	r, e := s.DB.QueryContext(ctx, "SELECT id,name,path,created_at FROM projects ORDER BY created_at DESC")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []model.Project{}
	for r.Next() {
		var x model.Project
		if e = r.Scan(&x.ID, &x.Name, &x.Path, &x.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, r.Err()
}
func (s *Store) CreateProject(ctx context.Context, x model.Project) (model.Project, error) {
	if !valid(x.Name, 160, true) || !valid(x.Path, 4096, true) || !filepath.IsAbs(x.Path) {
		return x, ErrInvalid
	}
	x.ID = ID()
	x.CreatedAt = now()
	_, e := s.DB.ExecContext(ctx, "INSERT INTO projects VALUES(?,?,?,?)", x.ID, x.Name, x.Path, x.CreatedAt)
	return x, fk(e)
}
func (s *Store) GetOrCreateProject(ctx context.Context, x model.Project) (model.Project, error) {
	if !valid(x.Name, 160, true) || !valid(x.Path, 4096, true) || !filepath.IsAbs(x.Path) {
		return x, ErrInvalid
	}
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return x, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return x, err
	}
	committed := false
	defer func() {
		if !committed {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _ = conn.ExecContext(cleanupCtx, "ROLLBACK")
		}
	}()
	err = conn.QueryRowContext(ctx, "SELECT id,name,path,created_at FROM projects WHERE path=? ORDER BY created_at,id LIMIT 1", x.Path).Scan(&x.ID, &x.Name, &x.Path, &x.CreatedAt)
	if err == nil {
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			return x, err
		}
		committed = true
		return x, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return x, err
	}
	x.ID = ID()
	x.CreatedAt = now()
	if _, err = conn.ExecContext(ctx, "INSERT INTO projects VALUES(?,?,?,?)", x.ID, x.Name, x.Path, x.CreatedAt); err != nil {
		return x, fk(err)
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return x, err
	}
	committed = true
	return x, nil
}
func (s *Store) UpdateProject(ctx context.Context, x model.Project) (model.Project, error) {
	if !valid(x.Name, 160, true) || !valid(x.Path, 4096, true) || !filepath.IsAbs(x.Path) {
		return x, ErrInvalid
	}
	r, e := s.DB.ExecContext(ctx, "UPDATE projects SET name=?,path=? WHERE id=?", x.Name, x.Path, x.ID)
	if e != nil {
		return x, fk(e)
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return x, ErrNotFound
	}
	return x, nil
}
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	r, e := s.DB.ExecContext(ctx, "DELETE FROM projects WHERE id=?", id)
	if e != nil {
		return fk(e)
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) GetProject(ctx context.Context, id string) (model.Project, error) {
	var x model.Project
	e := s.DB.QueryRowContext(ctx, "SELECT id,name,path,created_at FROM projects WHERE id=?", id).Scan(&x.ID, &x.Name, &x.Path, &x.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, e
}
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (s *Store) ListTasks(ctx context.Context) ([]model.Task, error) {
	r, e := s.DB.QueryContext(ctx, "SELECT id,title,description,COALESCE(project_id,''),session_id,status,due_date,created_at,updated_at FROM tasks ORDER BY created_at DESC")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []model.Task{}
	for r.Next() {
		var x model.Task
		if e = r.Scan(&x.ID, &x.Title, &x.Description, &x.ProjectID, &x.SessionID, &x.Status, &x.DueDate, &x.CreatedAt, &x.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, r.Err()
}
func validateTask(x model.Task) error {
	if !valid(x.Title, 240, true) || !valid(x.Description, 20000, false) || !valid(x.SessionID, 128, false) || !valid(x.ProjectID, 128, false) || !(x.Status == "todo" || x.Status == "doing" || x.Status == "done") || !valid(x.DueDate, 40, false) {
		return ErrInvalid
	}
	if x.DueDate != "" {
		if _, e := time.Parse("2006-01-02", x.DueDate); e != nil {
			return ErrInvalid
		}
	}
	return nil
}
func (s *Store) CreateTask(ctx context.Context, x model.Task) (model.Task, error) {
	if x.Status == "" {
		x.Status = "todo"
	}
	if e := validateTask(x); e != nil {
		return x, e
	}
	x.ID = ID()
	x.CreatedAt = now()
	x.UpdatedAt = x.CreatedAt
	_, e := s.DB.ExecContext(ctx, "INSERT INTO tasks VALUES(?,?,?,?,?,?,?,?,?)", x.ID, x.Title, x.Description, nullable(x.ProjectID), x.SessionID, x.Status, x.DueDate, x.CreatedAt, x.UpdatedAt)
	return x, fk(e)
}
func (s *Store) UpdateTask(ctx context.Context, x model.Task) (model.Task, error) {
	if e := validateTask(x); e != nil {
		return x, e
	}
	x.UpdatedAt = now()
	r, e := s.DB.ExecContext(ctx, "UPDATE tasks SET title=?,description=?,project_id=?,session_id=?,status=?,due_date=?,updated_at=? WHERE id=?", x.Title, x.Description, nullable(x.ProjectID), x.SessionID, x.Status, x.DueDate, x.UpdatedAt, x.ID)
	if e != nil {
		return x, fk(e)
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return x, ErrNotFound
	}
	return x, nil
}
func (s *Store) DeleteTask(ctx context.Context, id string) error { return del(ctx, s.DB, "tasks", id) }
func (s *Store) GetTask(ctx context.Context, id string) (model.Task, error) {
	var x model.Task
	e := s.DB.QueryRowContext(ctx, "SELECT id,title,description,COALESCE(project_id,''),session_id,status,due_date,created_at,updated_at FROM tasks WHERE id=?", id).Scan(&x.ID, &x.Title, &x.Description, &x.ProjectID, &x.SessionID, &x.Status, &x.DueDate, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, e
}
func (s *Store) ListNotes(ctx context.Context) ([]model.Note, error) {
	r, e := s.DB.QueryContext(ctx, "SELECT id,title,body,COALESCE(project_id,''),version,created_at,updated_at FROM notes ORDER BY updated_at DESC")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []model.Note{}
	for r.Next() {
		var x model.Note
		if e = r.Scan(&x.ID, &x.Title, &x.Body, &x.ProjectID, &x.Version, &x.CreatedAt, &x.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, r.Err()
}
func validateNote(x model.Note) error {
	if !valid(x.Title, 240, true) || !valid(x.Body, 1000000, false) || !valid(x.ProjectID, 128, false) {
		return ErrInvalid
	}
	return nil
}
func (s *Store) CreateNote(ctx context.Context, x model.Note) (model.Note, error) {
	if e := validateNote(x); e != nil {
		return x, e
	}
	x.ID = ID()
	x.Version = 1
	x.CreatedAt = now()
	x.UpdatedAt = x.CreatedAt
	_, e := s.DB.ExecContext(ctx, "INSERT INTO notes VALUES(?,?,?,?,?,?,?)", x.ID, x.Title, x.Body, nullable(x.ProjectID), x.Version, x.CreatedAt, x.UpdatedAt)
	return x, fk(e)
}
func (s *Store) UpdateNote(ctx context.Context, x model.Note) (model.Note, error) {
	if e := validateNote(x); e != nil {
		return x, e
	}
	x.UpdatedAt = now()
	r, e := s.DB.ExecContext(ctx, "UPDATE notes SET title=?,body=?,project_id=?,version=version+1,updated_at=? WHERE id=? AND version=?", x.Title, x.Body, nullable(x.ProjectID), x.UpdatedAt, x.ID, x.Version)
	if e != nil {
		return x, fk(e)
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		current, err := s.GetNote(ctx, x.ID)
		if err != nil {
			return current, err
		}
		return current, ErrConflict
	}
	x.Version++
	return x, nil
}
func (s *Store) GetNote(ctx context.Context, id string) (model.Note, error) {
	var x model.Note
	e := s.DB.QueryRowContext(ctx, "SELECT id,title,body,COALESCE(project_id,''),version,created_at,updated_at FROM notes WHERE id=?", id).Scan(&x.ID, &x.Title, &x.Body, &x.ProjectID, &x.Version, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, e
}
func (s *Store) DeleteNote(ctx context.Context, id string) error { return del(ctx, s.DB, "notes", id) }
func del(ctx context.Context, db *sql.DB, table, id string) error {
	r, e := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE id=?", id)
	if e != nil {
		return fk(e)
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) Preferences(ctx context.Context) (model.Preferences, error) {
	var x model.Preferences
	e := s.DB.QueryRowContext(ctx, "SELECT theme FROM preferences WHERE id=1").Scan(&x.Theme)
	return x, e
}
func (s *Store) SetPreferences(ctx context.Context, x model.Preferences) (model.Preferences, error) {
	if x.Theme != "system" && x.Theme != "dark" && x.Theme != "light" {
		return x, ErrInvalid
	}
	_, e := s.DB.ExecContext(ctx, "UPDATE preferences SET theme=? WHERE id=1", x.Theme)
	return x, e
}
