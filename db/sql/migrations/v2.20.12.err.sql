-- Legacy template grants identify built-ins by role_slug. Preserve their
-- allow/deny masks while returning built-in IDs to that representation.
update `project__template_role`
set `role_slug`=lower(substr(`role_id`, 9)),
    `role_id`=null
where `role_id` like 'builtin:%';

drop table `project__user_role`;
