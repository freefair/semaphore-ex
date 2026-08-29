drop index if exists `project__workflow_run__desired_state`;

alter table `project__workflow_run` drop column `desired_state`;
