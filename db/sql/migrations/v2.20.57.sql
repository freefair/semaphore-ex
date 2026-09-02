alter table `task` add column `global_credential_bindings` text not null default '{}';

create table `global_credential_usage` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `task_id` int not null, `project_id` int not null, `actor_id` int not null,
 `runner_id` int null, `dispatch_generation` int not null default 0,
 `target` varchar(128) not null, `credential_id` int not null, `grant_id` int not null default 0,
 `credential_version` int not null default 0, `version_fingerprint` varchar(64) not null default '',
 `provider_version` int not null default 0, `outcome` varchar(16) not null,
 `reason` varchar(64) not null, `occurred_at` datetime not null
);
create index `global_credential_usage__credential_history`
 on `global_credential_usage` (`credential_id`, `id`);
create index `global_credential_usage__credential_project`
 on `global_credential_usage` (`credential_id`, `project_id`, `id`);
create index `global_credential_usage__task`
 on `global_credential_usage` (`project_id`, `task_id`, `id`);
