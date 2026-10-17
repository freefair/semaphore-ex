-- Upstream 2.20.12 is adapted for Semaphore EX. The upstream table swap
-- discards EX immutable role IDs, CAS revisions, scoped permissions, and
-- directory-assignment ownership. EX retains those durable contracts and adds
-- an independent multi-role relation instead.
create table `project__user_role` (
    `project_id` int not null,
    `user_id` int not null,
    `role_id` varchar(64) not null,
    `ldap_group_managed_assignment_id` int null,
    `oidc_group_managed_assignment_id` int null,
    primary key (`project_id`, `user_id`, `role_id`),
    foreign key (`project_id`) references project (`id`) on delete cascade,
    foreign key (`user_id`) references `user` (`id`) on delete cascade,
    check (`ldap_group_managed_assignment_id` is null or `oidc_group_managed_assignment_id` is null)
);
create index `project__user_role__user` on `project__user_role` (`user_id`);
create index `project__user_role__ldap_assignment` on `project__user_role` (`ldap_group_managed_assignment_id`);
create index `project__user_role__oidc_assignment` on `project__user_role` (`oidc_group_managed_assignment_id`);

-- A legacy built-in assignment is represented by a stable EX reference; a
-- project/global custom roles are resolved from their historical slug.
-- PreApply rejects any unresolved slug.
update `project__user` set `role`=lower(`role`)
where `role_id` is null and lower(`role`) in ('owner', 'manager', 'task_runner', 'guest');

insert into `project__user_role` (`project_id`, `user_id`, `role_id`, `ldap_group_managed_assignment_id`, `oidc_group_managed_assignment_id`)
select `project_id`, `user_id`,
	   case when `role_id` is not null then `role_id`
	        when lower(`role`) in ('owner', 'manager', 'task_runner', 'guest') then {{if .Mysql}}concat('builtin:', lower(`role`)){{else}}'builtin:' || lower(`role`){{end}}
	        else (select r.`role_id` from `role` r where (r.`project_id`=`project__user`.`project_id` or r.`project_id` is null) and lower(r.`slug`)=lower(`project__user`.`role`) order by r.`project_id` is null limit 1) end,
       `ldap_group_managed_assignment_id`, `oidc_group_managed_assignment_id`
from `project__user`;
