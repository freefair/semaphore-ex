update `role` set `permissions` = `permissions` & ~1048576;
update `project__workflow_role` set `permissions` = `permissions` & ~1048576;
