alter table `docker_reconciliation_session` add column `scan_cursor` bigint not null default 0;
alter table `docker_reconciliation_scan_target` add column `sequence` bigint not null default 0;
