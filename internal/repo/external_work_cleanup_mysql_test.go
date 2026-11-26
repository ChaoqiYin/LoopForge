package repo

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// The cleanup migrates legacy requirement data to external Work Items. It must
// never run against the database named by the DSN: every statement is applied
// to a throwaway schema created for this test. The script itself is the only
// thing under test.
func TestMySQLExternalWorkCleanupMigration(t *testing.T) {
	dsn := os.Getenv("AGENTFLOW_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTFLOW_TEST_MYSQL_DSN is not set")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MultiStatements = true
	admin, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx := t.Context()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatal(err)
	}

	schema := fmt.Sprintf("loopforge_cleanup_043_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+schema+"`"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+schema+"`") }()

	config.DBName = schema
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, cleanupTestSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, cleanupTestData); err != nil {
		t.Fatal(err)
	}

	preview, err := os.ReadFile("../../migrations/043_external_work_cleanup_dry_run.sql")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.QueryContext(ctx, string(preview))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for rows.Next() {
		var target string
		var pending int64
		if err := rows.Scan(&target, &pending); err != nil {
			t.Fatal(err)
		}
		got[target] = pending
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	want := map[string]int64{
		"issues": 2, "issue_comments": 2, "requirement_spaces": 2,
		"bot_clarify_sessions": 2, "tasks_preserved": 2, "runs_preserved": 2,
		"run_events_preserved": 2,
	}
	for target, pending := range want {
		if got[target] != pending {
			t.Fatalf("dry-run %s = %d, want %d (all counts: %v)", target, got[target], pending, got)
		}
	}

	for attempt := 1; attempt <= 2; attempt++ {
		execCleanupSQL(t, ctx, conn, "../../migrations/043_external_work_cleanup.sql")
		assertCleanupState(t, ctx, conn, attempt)
	}
}

func execCleanupSQL(t *testing.T, ctx context.Context, conn *sql.Conn, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		lines = append(lines, line)
	}
	for _, stmt := range strings.Split(strings.Join(lines, "\n"), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, stmt)
		}
	}
}

func assertCleanupState(t *testing.T, ctx context.Context, conn *sql.Conn, attempt int) {
	t.Helper()
	queryCount := func(query string) int64 {
		var n int64
		if err := conn.QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatalf("attempt %d: %v\n%s", attempt, err, query)
		}
		return n
	}
	if n := queryCount(`SELECT COUNT(*) FROM issues`); n != 1 {
		t.Fatalf("attempt %d: issues = %d, want the formal Work Item only", attempt, n)
	}
	var source string
	if err := conn.QueryRowContext(ctx, `SELECT source FROM issues`).Scan(&source); err != nil || source != "feishu_bitable" {
		t.Fatalf("attempt %d: kept issue source = %q, err=%v", attempt, source, err)
	}
	if n := queryCount(`SELECT COUNT(*) FROM issue_comments`); n != 1 {
		t.Fatalf("attempt %d: issue_comments = %d, want the formal comment only", attempt, n)
	}
	if n := queryCount(`SELECT COUNT(*) FROM requirement_spaces`); n != 2 {
		t.Fatalf("attempt %d: requirement_spaces = %d, want the formal and unlinked spaces", attempt, n)
	}
	if n := queryCount(`SELECT COUNT(*) FROM bot_clarify_sessions`); n != 1 {
		t.Fatalf("attempt %d: bot_clarify_sessions = %d, want the formal session only", attempt, n)
	}
	// Execution evidence is never deleted and may keep a dangling issue_id.
	if n := queryCount(`SELECT COUNT(*) FROM tasks`); n != 2 {
		t.Fatalf("attempt %d: tasks = %d", attempt, n)
	}
	if n := queryCount(`SELECT COUNT(*) FROM runs`); n != 2 {
		t.Fatalf("attempt %d: runs = %d", attempt, n)
	}
	if n := queryCount(`SELECT COUNT(*) FROM run_events`); n != 2 {
		t.Fatalf("attempt %d: run_events = %d", attempt, n)
	}
	var dangling string
	if err := conn.QueryRowContext(ctx, `SELECT issue_id FROM tasks WHERE id = 'task-local'`).Scan(&dangling); err != nil || dangling != "issue-local" {
		t.Fatalf("attempt %d: preserved task lost its issue_id: %q %v", attempt, dangling, err)
	}
	for table, want := range map[string]int64{
		"issues_cleanup_043_backup": 2, "issue_comments_cleanup_043_backup": 2,
		"requirement_spaces_cleanup_043_backup": 2, "bot_clarify_sessions_cleanup_043_backup": 2,
	} {
		if n := queryCount(`SELECT COUNT(*) FROM ` + table); n != want {
			t.Fatalf("attempt %d: %s = %d, want %d", attempt, table, n, want)
		}
	}
	if n := queryCount(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name IN ('issues','issue_comments','requirement_spaces','bot_clarify_sessions','tasks','runs','run_events')`); n != 7 {
		t.Fatalf("attempt %d: a business table was dropped", attempt)
	}
}

const cleanupTestSchema = `
CREATE TABLE issues (
  id VARCHAR(64) PRIMARY KEY,
  project_id VARCHAR(64) NOT NULL,
  source VARCHAR(32) NULL,
  kind VARCHAR(32) NULL,
  status VARCHAR(32) NULL,
  payload JSON NULL,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE issue_comments (
  id VARCHAR(64) PRIMARY KEY,
  issue_id VARCHAR(64) NOT NULL,
  payload JSON NULL,
  created_at DATETIME(3) NULL,
  INDEX idx_comments_issue (issue_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE requirement_spaces (
  id VARCHAR(64) PRIMARY KEY,
  project_id VARCHAR(64) NOT NULL,
  issue_id VARCHAR(64) NOT NULL,
  payload JSON NULL,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL,
  INDEX idx_requirement_spaces_issue (issue_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE bot_clarify_sessions (
  id VARCHAR(64) PRIMARY KEY,
  project_id VARCHAR(191) NULL,
  conversation_id VARCHAR(64) NULL,
  issue_id VARCHAR(191) NOT NULL,
  status VARCHAR(16) NULL,
  payload MEDIUMTEXT NULL,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL,
  INDEX idx_bot_clarify_issue (issue_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE tasks (
  id VARCHAR(64) PRIMARY KEY,
  issue_id VARCHAR(64) NOT NULL,
  run_id VARCHAR(64) NOT NULL,
  status VARCHAR(32) NULL,
  payload JSON NULL,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE runs (
  id VARCHAR(64) PRIMARY KEY,
  status VARCHAR(32) NULL,
  payload JSON NULL,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE run_events (
  id VARCHAR(64) PRIMARY KEY,
  run_id VARCHAR(64) NOT NULL,
  payload JSON NULL,
  created_at DATETIME(3) NULL,
  INDEX idx_run_events_run (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

const cleanupTestData = `
INSERT INTO issues (id, project_id, source, kind, status, payload) VALUES
  ('issue-local', 'p', 'local', 'requirement', 'todo', JSON_OBJECT('id','issue-local')),
  ('issue-tapd', 'p', 'tapd', 'requirement', 'in_progress', JSON_OBJECT('id','issue-tapd')),
  ('issue-formal', 'p', 'feishu_bitable', 'requirement', 'todo', JSON_OBJECT('id','issue-formal'));
INSERT INTO issue_comments (id, issue_id, payload) VALUES
  ('comment-local', 'issue-local', JSON_OBJECT('id','comment-local')),
  ('comment-orphan', 'issue-deleted', JSON_OBJECT('id','comment-orphan')),
  ('comment-formal', 'issue-formal', JSON_OBJECT('id','comment-formal'));
INSERT INTO requirement_spaces (id, project_id, issue_id, payload) VALUES
  ('space-local', 'p', 'issue-local', JSON_OBJECT('id','space-local')),
  ('space-orphan', 'p', 'issue-deleted', JSON_OBJECT('id','space-orphan')),
  ('space-formal', 'p', 'issue-formal', JSON_OBJECT('id','space-formal')),
  ('space-plain', 'p', '', JSON_OBJECT('id','space-plain'));
INSERT INTO bot_clarify_sessions (id, project_id, conversation_id, issue_id, status, payload) VALUES
  ('clarify-local', 'p', 'c1', 'issue-local', 'ended', '{"summary":"local"}'),
  ('clarify-unlinked', 'p', 'c2', '', 'analyzing', '{"summary":"open"}'),
  ('clarify-formal', 'p', 'c3', 'issue-formal', 'ended', '{"summary":"formal"}');
INSERT INTO tasks (id, issue_id, run_id, status, payload) VALUES
  ('task-local', 'issue-local', 'run-local', 'succeeded', JSON_OBJECT('id','task-local')),
  ('task-formal', 'issue-formal', 'run-formal', 'running', JSON_OBJECT('id','task-formal'));
INSERT INTO runs (id, status, payload) VALUES
  ('run-local', 'succeeded', JSON_OBJECT('id','run-local')),
  ('run-formal', 'running', JSON_OBJECT('id','run-formal'));
INSERT INTO run_events (id, run_id, payload) VALUES
  ('event-local', 'run-local', JSON_OBJECT('id','event-local')),
  ('event-formal', 'run-formal', JSON_OBJECT('id','event-formal'));
`
