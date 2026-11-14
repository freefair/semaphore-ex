update `project__workflow_role`
set `permissions` = `permissions` | 2097152
where `role_id` = 'builtin:guest' and `permissions` <> 0;
alter table `user` drop column `disabled`;
