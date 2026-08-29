drop index if exists `project__workflow_run__reconciliation`;

alter table `project__workflow_run` drop column `reconciliation_quarantined_at`;
alter table `project__workflow_run` drop column `reconciliation_next_retry_at`;
alter table `project__workflow_run` drop column `reconciliation_last_error`;
alter table `project__workflow_run` drop column `reconciliation_attempts`;
alter table `project__workflow_run` drop column `reconciliation_state`;
