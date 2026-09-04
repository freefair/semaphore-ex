{{ if .Mysql }}drop index `project__workflow_run__reconciliation` on `project__workflow_run`{{ else }}drop index if exists `project__workflow_run__reconciliation`{{ end }};

alter table `project__workflow_run` drop column `reconciliation_quarantined_at`;
alter table `project__workflow_run` drop column `reconciliation_next_retry_at`;
alter table `project__workflow_run` drop column `reconciliation_last_error`;
alter table `project__workflow_run` drop column `reconciliation_attempts`;
alter table `project__workflow_run` drop column `reconciliation_state`;
