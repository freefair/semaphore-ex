alter table `docker_reconciliation_session` add column `telemetry_highest_sequence` bigint not null default 0;
create table `docker_telemetry_event` (
 `session_id` varchar(128) not null, `sequence` bigint not null, `fingerprint` varchar(64) not null,
 primary key (`session_id`, `sequence`)
);
