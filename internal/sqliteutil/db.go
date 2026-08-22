package sqliteutil

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liliang-cn/tagit/internal/tagitpath"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO) — registers driver name "sqlite"
)

// DBPath returns the canonical SQLite database path for a workspace.
func DBPath(workDir string) string {
	return tagitpath.Join(workDir, "tagit.db")
}

// A *sql.DB is a connection pool meant to be long-lived and shared. Callers open
// a store per operation (recovery loop, per-request handlers, per-run); opening a
// fresh pool each time and never closing it leaks one db + one -wal file
// descriptor per call, which took the daemon down after ~1 day of uptime. So we
// hand out a single shared, process-lived pool per database path. Nothing closes
// it; process exit reclaims the handles.
var (
	dbCacheMu sync.Mutex
	dbCache   = make(map[string]*sql.DB)
)

// Open returns a shared SQLite handle for the workspace, applying the base schema
// on first open. Subsequent opens of the same path reuse the cached pool.
func Open(workDir string) (*sql.DB, error) {
	dbPath := DBPath(workDir)

	dbCacheMu.Lock()
	defer dbCacheMu.Unlock()
	if db, ok := dbCache[dbPath]; ok {
		return db, nil
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if _, err := db.Exec(`
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
-- Without a busy timeout SQLite gives up the instant another writer holds the
-- lock, and the caller sees "database is locked (5)" rather than a short wait.
-- TagIt writes events and artifacts from every agent in a run, so parallel and
-- senate modes have several writers by design: a run would fail on contention
-- that lasts milliseconds.
PRAGMA busy_timeout = 5000;
CREATE TABLE IF NOT EXISTS event_records (
  id TEXT PRIMARY KEY,
  session_id TEXT,
  task_id TEXT,
  type TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  occurred_at TEXT NOT NULL,
  reason_code TEXT,
  payload_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_event_records_session ON event_records(session_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_event_records_task ON event_records(task_id, occurred_at);

CREATE TABLE IF NOT EXISTS session_history (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  prompt TEXT NOT NULL,
  starter TEXT NOT NULL,
  delegates_json TEXT,
  working_dir TEXT NOT NULL,
  status TEXT NOT NULL,
  artifact_ids_json TEXT,
  final_artifact_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_history_created ON session_history(created_at);

CREATE TABLE IF NOT EXISTS task_records (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  title TEXT NOT NULL,
  strategy TEXT NOT NULL,
  state TEXT NOT NULL,
  agent_id TEXT,
  approval_granted INTEGER NOT NULL DEFAULT 0,
  dependencies_json TEXT,
  artifact_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_records_session ON task_records(session_id, created_at);

CREATE TABLE IF NOT EXISTS queue_requests (
  id TEXT PRIMARY KEY,
  graph_file TEXT,
  graph_json TEXT,
  prompt TEXT NOT NULL,
  mode TEXT,
  starter_agent TEXT NOT NULL,
  delegates_json TEXT,
  working_dir TEXT NOT NULL,
  continuous INTEGER NOT NULL DEFAULT 0,
  max_rounds INTEGER NOT NULL DEFAULT 0,
  session_id TEXT,
  task_id TEXT,
  artifact_ids_json TEXT,
  policy_override INTEGER NOT NULL DEFAULT 0,
  policy_override_actor TEXT,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  error TEXT
);
CREATE INDEX IF NOT EXISTS idx_queue_requests_created ON queue_requests(created_at);
CREATE INDEX IF NOT EXISTS idx_queue_requests_status_created ON queue_requests(status, created_at);

CREATE TABLE IF NOT EXISTS artifact_records (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  created_at TEXT NOT NULL,
  envelope_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifact_records_session ON artifact_records(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_artifact_records_task ON artifact_records(task_id, created_at);

CREATE TABLE IF NOT EXISTS scheduler_leases (
  session_id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  status TEXT NOT NULL,
  ready_task_ids_json TEXT,
  workspace_refs_json TEXT,
  pending_approval_task_ids_json TEXT,
  completed_task_ids_json TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scheduler_leases_status_updated ON scheduler_leases(status, updated_at);
`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE queue_requests ADD COLUMN policy_override INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate queue_requests.policy_override: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE queue_requests ADD COLUMN policy_override_actor TEXT`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate queue_requests.policy_override_actor: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE queue_requests ADD COLUMN continuous INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate queue_requests.continuous: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE queue_requests ADD COLUMN max_rounds INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate queue_requests.max_rounds: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE queue_requests ADD COLUMN mode TEXT`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate queue_requests.mode: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE session_history ADD COLUMN final_artifact_id TEXT`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate session_history.final_artifact_id: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE task_records ADD COLUMN approval_granted INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate task_records.approval_granted: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE scheduler_leases ADD COLUMN workspace_refs_json TEXT`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate scheduler_leases.workspace_refs_json: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE scheduler_leases ADD COLUMN pending_approval_task_ids_json TEXT`); err != nil {
		if !alreadyExists(err) {
			_ = db.Close()
			return nil, fmt.Errorf("migrate scheduler_leases.pending_approval_task_ids_json: %w", err)
		}
	}
	dbCache[dbPath] = db
	return db, nil
}

func alreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}
