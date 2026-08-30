create table `docker_reconciliation_orphan_candidate` (
 `session_id` varchar(128) not null, `runner_id` int not null,
 `resource` varchar(16) not null, `identifier` varchar(128) not null, `name` varchar(128) not null,
 `reason` varchar(32) not null, `fingerprint` varchar(64) not null, `observed_at` datetime not null,
 primary key (`session_id`, `fingerprint`)
);
create index `docker_reconciliation_orphan_candidate__runner` on `docker_reconciliation_orphan_candidate` (`runner_id`, `observed_at`);
