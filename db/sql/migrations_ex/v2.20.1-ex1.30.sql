alter table `ldap_provider` add column `group_search_base_dn` varchar(1000) not null default '';
alter table `ldap_provider` add column `group_user_filter` varchar(1000) not null default '';
alter table `ldap_provider` add column `group_filter` varchar(1000) not null default '';
alter table `ldap_provider` add column `group_identity_attribute` varchar(100) not null default '';
alter table `ldap_provider` add column `group_member_attribute` varchar(100) not null default '';
alter table `ldap_provider` add column `group_max_depth` int not null default 0;

create table `ldap_group_mapping_state` (
  `provider_id` varchar(64) primary key,
  `revision` int not null default 1,
  foreign key (`provider_id`) references `ldap_provider` (`id`) on delete cascade
);
insert into `ldap_group_mapping_state` (`provider_id`, `revision`)
select `id`, 1 from `ldap_provider`;

create table `ldap_group_mapping` (
  `id` varchar(64) not null,
  `provider_id` varchar(64) not null,
  `group_external_id` varchar(100) not null,
  `target_scope` varchar(16) not null,
  `project_id` int null,
  `role_id` varchar(64) not null,
  `enabled` boolean not null default true,
  `revision` int not null default 1,
  `created` datetime not null,
  `updated` datetime not null,
  primary key (`provider_id`, `id`),
  foreign key (`provider_id`) references `ldap_provider` (`id`) on delete cascade,
  foreign key (`project_id`) references `project` (`id`) on delete cascade
);
create index `ldap_group_mapping__group` on `ldap_group_mapping` (`provider_id`, `group_external_id`);
create index `ldap_group_mapping__target` on `ldap_group_mapping` (`target_scope`, `project_id`, `role_id`);

create table `ldap_group_managed_assignment` (
  `id` integer primary key autoincrement,
  `provider_id` varchar(64) not null,
  `mapping_id` varchar(64) not null,
  `user_id` int not null,
  `target_scope` varchar(16) not null,
  `project_id` int null,
  `role_id` varchar(64) not null,
  `global_assignment_id` int null,
  `created` datetime not null,
  foreign key (`provider_id`) references `ldap_provider` (`id`) on delete cascade,
  foreign key (`user_id`) references `user` (`id`) on delete cascade,
  foreign key (`project_id`) references `project` (`id`) on delete cascade,
  foreign key (`global_assignment_id`) references `user__global_role` (`id`) on delete cascade,
  unique (`provider_id`, `mapping_id`, `user_id`, `target_scope`, `role_id`)
);
create index `ldap_group_managed_assignment__user` on `ldap_group_managed_assignment` (`provider_id`, `user_id`);
create index `ldap_group_managed_assignment__project` on `ldap_group_managed_assignment` (`project_id`, `user_id`);

alter table `project__user` add column `ldap_group_managed_assignment_id` int null;
create index `project__user__ldap_group_managed_assignment` on `project__user` (`ldap_group_managed_assignment_id`);

create table `ldap_group_reconciliation` (
  `id` integer primary key autoincrement,
  `provider_id` varchar(64) not null,
  `source` varchar(16) not null,
  `status` varchar(16) not null,
  `token` varchar(64) not null,
  `mapping_revision` int not null,
  `directory_revision` varchar(64) not null,
  `preview_json` longtext not null,
  `addition_count` int not null default 0,
  `removal_count` int not null default 0,
  `unresolved_count` int not null default 0,
  `collision_count` int not null default 0,
  `protected_admin_count` int not null default 0,
  `error_code` varchar(64) not null default '',
  `actor_id` int null,
  `created` datetime not null,
  `applied_at` datetime null,
  foreign key (`provider_id`) references `ldap_provider` (`id`) on delete cascade,
  foreign key (`actor_id`) references `user` (`id`) on delete set null
);
create index `ldap_group_reconciliation__provider` on `ldap_group_reconciliation` (`provider_id`, `id`);
create index `ldap_group_reconciliation__token` on `ldap_group_reconciliation` (`provider_id`, `token`);
