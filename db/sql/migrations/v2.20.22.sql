alter table `project__workflow_run` add column `reconciliation_state` varchar(20) not null default 'healthy';
alter table `project__workflow_run` add column `reconciliation_attempts` int not null default 0;
alter table `project__workflow_run` add column `reconciliation_last_error` text not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_run` add column `reconciliation_next_retry_at` datetime null;
alter table `project__workflow_run` add column `reconciliation_quarantined_at` datetime null;

create index `project__workflow_run__reconciliation`
  on `project__workflow_run`(`reconciliation_state`, `reconciliation_next_retry_at`, `id`);
