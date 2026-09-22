{{ if .Mysql }}
alter table `project__workflow_template` add column `access_policy` longtext null;
update `project__workflow_template` set `access_policy` = '{}' where `access_policy` is null;
alter table `project__workflow_template` modify column `access_policy` longtext not null;
{{ else }}
alter table `project__workflow_template` add column `access_policy` longtext not null default '{}';
{{ end }}
alter table `project__workflow_template` add column `access_policy_revision` int not null default 1;
{{ if .Mysql }}
alter table `project__workflow_node` add column `approval_role_policy` longtext null;
update `project__workflow_node` set `approval_role_policy` = '{}' where `approval_role_policy` is null;
alter table `project__workflow_node` modify column `approval_role_policy` longtext not null;
{{ else }}
alter table `project__workflow_node` add column `approval_role_policy` longtext not null default '{}';
{{ end }}
alter table `project__workflow_node` add column `approval_role_policy_revision` int not null default 1;
{{ if .Mysql }}
alter table `project__workflow_approval` add column `role_policy_snapshot` longtext null;
update `project__workflow_approval` set `role_policy_snapshot` = '{}' where `role_policy_snapshot` is null;
alter table `project__workflow_approval` modify column `role_policy_snapshot` longtext not null;
{{ else }}
alter table `project__workflow_approval` add column `role_policy_snapshot` longtext not null default '{}';
{{ end }}
alter table `project__workflow_approval` add column `role_policy_revision` int not null default 0;
create table `project__workflow_approval_contribution` (
 `workflow_approval_id` int not null, `actor_user_id` int not null, `role_id` varchar(128) not null,
 `role_revision` int not null, `role_origin` varchar(16) not null,
 `directory_provider_id` varchar(64) not null default '', `directory_mapping_id` varchar(64) not null default '',
 `directory_mapping_revision` int not null default 0,
 `directory_revision_fingerprint` varchar(64) not null default '',
 `decision` varchar(16) not null, `comment` text not null{{ if not .Mysql }} default ''{{ end }},
 `created` datetime not null, `policy_revision` int not null, `correlation_id` varchar(128) not null,
 primary key (`workflow_approval_id`, `actor_user_id`),
 foreign key (`workflow_approval_id`) references `project__workflow_approval`(`id`) on delete cascade
);
create index `project__workflow_approval_contribution__actor`
 on `project__workflow_approval_contribution` (`actor_user_id`, `created`);
update `role` set `permissions` = `permissions`
 | case when (`permissions` & 16) = 16 then 32 else 0 end
 | case when (`permissions` & 4) = 4 then 64 else 0 end
 | case when (`permissions` & 1) = 1 then 128 else 0 end
 | case when (`permissions` & 1) = 1 then 256 else 0 end
 | case when (`permissions` & 4) = 4 then 512 else 0 end
 where `project_id` is not null;
