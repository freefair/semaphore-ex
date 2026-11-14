-- Account lifecycle is durable and therefore works consistently across every
-- API node. Existing Guest workflow grants lose approval only; other roles and
-- grants remain unchanged.
alter table `user` add column `disabled` bool not null default false;
update `project__workflow_role`
set `permissions` = `permissions` & ~2097152
where `role_id` = 'builtin:guest';
