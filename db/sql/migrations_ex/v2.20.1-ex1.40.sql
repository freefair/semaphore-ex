alter table `docker_reconciliation_orphan_candidate` add column `revision` bigint not null default 1;
alter table `docker_reconciliation_orphan_candidate` add column `status` varchar(16) not null default 'pending';
alter table `docker_reconciliation_orphan_candidate` add column `remediated_at` datetime null;
create table `docker_reconciliation_remediation_command` (
 `command_id` varchar(128) primary key, `session_id` varchar(128) not null, `runner_id` int not null,
 `fence_hash` varchar(64) not null, `idempotency_key` varchar(128) not null,
 `action` varchar(32) not null, `target` varchar(16) not null, `expected_revision` bigint not null,
 `fingerprint` varchar(64) not null, `daemon_id` varchar(128) not null, `candidate_resource` varchar(16) not null default '',
 `runner_boot` varchar(128) not null default '', `project_id` int not null default 0, `task_id` int not null default 0,
 `generation` int not null default 0, `resource` varchar(32) not null default '',
 `status` varchar(16) not null default 'pending', `evidence` varchar(32) not null default '',
 `created_at` datetime not null, `reported_at` datetime null,
 unique (`runner_id`, `idempotency_key`)
);
create index `docker_reconciliation_remediation_command__session` on `docker_reconciliation_remediation_command` (`session_id`, `runner_id`, `status`);
