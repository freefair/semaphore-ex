{{ if .Mysql }}drop index `project__workflow_run__desired_state` on `project__workflow_run`{{ else }}drop index if exists `project__workflow_run__desired_state`{{ end }};

alter table `project__workflow_run` drop column `desired_state`;
