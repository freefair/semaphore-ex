-- Preserve existing project-wide visibility. EX template grants already have
-- separate allow/deny masks and must not be widened here.
update `role` set `permissions` = `permissions` | 1048576;
update `project__workflow_role` set `permissions` = `permissions` | 1048576 where `permissions` <> 0;
