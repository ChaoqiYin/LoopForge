-- One-time cleanup for the external Work Item cutover.
--
-- This script removes the legacy locally managed requirement data:
-- issues, issue_comments, requirement_spaces and chat clarification sessions.
-- It does not delete tables. It never touches tasks, runs, run_events or run
-- trace evidence, so those rows may keep referring to a deleted issue_id.
--
-- A Work Item projected from the Management Connection has
-- source = 'feishu_bitable'. Those rows, their comments, their collaboration
-- spaces and their clarification sessions are preserved, which makes the
-- script idempotent and safe to run again after go-live.
--
-- Before running it:
--   1. Stop every process that writes the database.
--   2. Take a full dump, for example:
--        mysqldump --single-transaction --routines --triggers \
--          -u <user> -p <database> > loopforge-before-043.sql
--   3. Run 043_external_work_cleanup_dry_run.sql and review every count.
--
-- The *_cleanup_043_backup tables copy the removed rows before deletion. They
-- are the in-database rollback copy for the rollback window. Drop them only
-- after that window and after a verified external Work Item sync.

CREATE TABLE IF NOT EXISTS issues_cleanup_043_backup LIKE issues;

INSERT IGNORE INTO issues_cleanup_043_backup
SELECT * FROM issues
WHERE source IS NULL OR source <> 'feishu_bitable';

CREATE TABLE IF NOT EXISTS issue_comments_cleanup_043_backup LIKE issue_comments;

INSERT IGNORE INTO issue_comments_cleanup_043_backup
SELECT c.* FROM issue_comments c
WHERE NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = c.issue_id AND i.source = 'feishu_bitable'
);

CREATE TABLE IF NOT EXISTS requirement_spaces_cleanup_043_backup LIKE requirement_spaces;

INSERT IGNORE INTO requirement_spaces_cleanup_043_backup
SELECT rs.* FROM requirement_spaces rs
WHERE rs.issue_id <> '' AND NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = rs.issue_id AND i.source = 'feishu_bitable'
);

CREATE TABLE IF NOT EXISTS bot_clarify_sessions_cleanup_043_backup LIKE bot_clarify_sessions;

INSERT IGNORE INTO bot_clarify_sessions_cleanup_043_backup
SELECT cs.* FROM bot_clarify_sessions cs
WHERE cs.issue_id = '' OR NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = cs.issue_id AND i.source = 'feishu_bitable'
);

DELETE rs FROM requirement_spaces rs
WHERE rs.issue_id <> '' AND NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = rs.issue_id AND i.source = 'feishu_bitable'
);

DELETE cs FROM bot_clarify_sessions cs
WHERE cs.issue_id = '' OR NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = cs.issue_id AND i.source = 'feishu_bitable'
);

DELETE c FROM issue_comments c
WHERE NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = c.issue_id AND i.source = 'feishu_bitable'
);

DELETE FROM issues
WHERE source IS NULL OR source <> 'feishu_bitable';
