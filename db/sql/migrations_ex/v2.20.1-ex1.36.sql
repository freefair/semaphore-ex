create table `docker_reconciliation_observation` (
 `id` integer primary key autoincrement,
 `runner_id` int not null, `runner_boot` varchar(128) not null,
 `task_id` int not null default 0, `project_id` int not null default 0, `generation` int not null default 0,
 `resource` varchar(32) not null, `container_id` varchar(128) not null default '', `container_name` varchar(128) not null default '',
 `state` varchar(16) not null, `reason` varchar(128) not null default '', `sequence` bigint not null, `revision` int not null,
 `observed_at` datetime not null, `quarantine_status` varchar(16) not null default 'none', `remediation` varchar(16) not null default 'none',
 `remediation_reason` varchar(128) not null default '', `quarantined_at` datetime null, `remediated_at` datetime null,
 unique (`runner_id`, `runner_boot`, `sequence`)
);
create table `docker_reconciliation_runner` (
 `runner_id` int not null, `runner_boot` varchar(128) not null, `last_sequence` bigint not null,
 primary key (`runner_id`, `runner_boot`)
);
create table `docker_reconciliation_state` (
 `runner_id` int not null, `runner_boot` varchar(128) not null,
 `project_id` int not null, `task_id` int not null, `generation` int not null, `resource` varchar(32) not null,
 `revision` bigint not null, `latest_sequence` bigint not null,
 `container_id` varchar(128) not null default '', `container_name` varchar(128) not null default '',
 `state` varchar(16) not null, `reason` varchar(128) not null default '', `updated_at` datetime not null,
 `quarantine_status` varchar(16) not null default 'none', `remediation` varchar(16) not null default 'none',
 `remediation_reason` varchar(128) not null default '', `quarantined_at` datetime null, `remediated_at` datetime null,
 primary key (`runner_id`, `runner_boot`, `project_id`, `task_id`, `generation`, `resource`)
);
create index `docker_reconciliation_observation__task` on `docker_reconciliation_observation` (`project_id`, `task_id`, `generation`);
