alter table `runner` add column `k8s_namespace` varchar(63) not null default '';
create table `kubernetes_reconciliation_session` (
 `session_id` varchar(128) primary key, `runner_id` int not null, `cluster_alias` varchar(128) not null,
 `namespace` varchar(63) not null, `fence_hash` varchar(64) not null, `active` bool not null,
 `scan_complete` bool not null, `scan_revision` bigint not null, `created_at` datetime not null
);
create table `kubernetes_reconciliation_target` (
 `session_id` varchar(128) not null, `project_id` int not null, `task_id` int not null, `generation` int not null,
 `job_name` varchar(63) not null, `job_uid` varchar(128) not null, `pod_name` varchar(63) not null default '', `pod_uid` varchar(128) not null default '',
 `secret_name` varchar(63) not null, `secret_uid` varchar(128) not null, `network_policy_name` varchar(63) not null, `network_policy_uid` varchar(128) not null,
 `retention_deadline` datetime, `retention_state` varchar(16) not null default '', primary key (`session_id`,`project_id`,`task_id`,`generation`)
);
create table `kubernetes_reconciliation_observation` (
 `session_id` varchar(128) not null, `project_id` int not null, `task_id` int not null, `generation` int not null,
 `state` varchar(16) not null, `reason` varchar(128) not null default '', `revision` bigint not null, `observed_at` datetime not null,
 primary key (`session_id`,`project_id`,`task_id`,`generation`)
);
create table `kubernetes_reconciliation_candidate` (
 `session_id` varchar(128) not null, `resource` varchar(32) not null, `name` varchar(63) not null, `uid` varchar(128) not null,
 `reason` varchar(128) not null, `revision` bigint not null, `status` varchar(16) not null, `observed_at` datetime not null,
 primary key (`session_id`,`resource`,`uid`)
);
create table `kubernetes_reconciliation_command` (
 `command_id` varchar(128) primary key, `session_id` varchar(128) not null, `runner_id` int not null, `fence_hash` varchar(64) not null,
 `idempotency_key` varchar(128) not null, `action` varchar(64) not null,
 `project_id` int not null, `task_id` int not null, `generation` int not null, `expected_revision` bigint not null,
 `job_name` varchar(63) not null, `job_uid` varchar(128) not null, `pod_name` varchar(63) not null default '', `pod_uid` varchar(128) not null default '',
 `secret_name` varchar(63) not null, `secret_uid` varchar(128) not null, `network_policy_name` varchar(63) not null, `network_policy_uid` varchar(128) not null,
 `retention_deadline` datetime, `retention_state` varchar(16) not null, `status` varchar(16) not null, `evidence` varchar(32) not null default '',
 `created_at` datetime not null, `reported_at` datetime, unique (`runner_id`,`idempotency_key`)
);
