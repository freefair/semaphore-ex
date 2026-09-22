create table `docker_reconciliation_session` (
 `session_id` varchar(128) primary key, `runner_id` int not null,
 `fence_hash` varchar(64) not null, `target_boot` varchar(128) not null,
 `active` tinyint not null default 1, `scan_complete` tinyint not null default 0, `scan_highest_sequence` bigint not null default 0,
 `created_at` datetime not null
);
create index `docker_reconciliation_session__active_runner` on `docker_reconciliation_session` (`runner_id`, `active`);
create table `docker_reconciliation_scan_target` (
 `session_id` varchar(128) not null, `target_boot` varchar(128) not null,
 `project_id` int not null, `task_id` int not null, `generation` int not null, `resource` varchar(32) not null, `container_name` varchar(128) not null,
 primary key (`session_id`, `project_id`, `task_id`, `generation`, `resource`)
);
create table `docker_reconciliation_attempt` (
 `runner_id` int not null, `target_boot` varchar(128) not null,
 `project_id` int not null, `task_id` int not null, `generation` int not null, `resource` varchar(32) not null, `container_name` varchar(128) not null,
 primary key (`runner_id`, `project_id`, `task_id`, `generation`, `resource`)
);
create table `docker_reconciliation_scan_observation` (
 `session_id` varchar(128) not null, `target_boot` varchar(128) not null, `project_id` int not null, `task_id` int not null, `generation` int not null, `resource` varchar(32) not null, `sequence` bigint not null,
 primary key (`session_id`, `project_id`, `task_id`, `generation`, `resource`)
);
