-- Preview for 043_external_work_cleanup.sql. It changes no data.
-- Run it first and compare the counts with the row counts you expect.
-- "pending" is the number of business rows the cleanup will remove.
-- The preserved counts prove that execution evidence is out of scope.
-- A Work Item projected from the Management Connection has
-- source = 'feishu_bitable' and is always kept, so running the cleanup again
-- after cutover is safe.

SELECT 'issues' AS target, COUNT(*) AS pending FROM issues
WHERE source IS NULL OR source <> 'feishu_bitable'
UNION ALL
SELECT 'issue_comments', COUNT(*) FROM issue_comments c
WHERE NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = c.issue_id AND i.source = 'feishu_bitable'
)
UNION ALL
SELECT 'requirement_spaces', COUNT(*) FROM requirement_spaces rs
WHERE rs.issue_id <> '' AND NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = rs.issue_id AND i.source = 'feishu_bitable'
)
UNION ALL
SELECT 'bot_clarify_sessions', COUNT(*) FROM bot_clarify_sessions cs
WHERE cs.issue_id = '' OR NOT EXISTS (
  SELECT 1 FROM issues i WHERE i.id = cs.issue_id AND i.source = 'feishu_bitable'
)
UNION ALL
SELECT 'tasks_preserved', COUNT(*) FROM tasks
UNION ALL
SELECT 'runs_preserved', COUNT(*) FROM runs
UNION ALL
SELECT 'run_events_preserved', COUNT(*) FROM run_events;
