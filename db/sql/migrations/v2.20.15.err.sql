{{ if .Mysql }}drop index `task__workflow_run_node_unique` on `task`{{ else }}drop index if exists `task__workflow_run_node_unique`{{ end }};
alter table `task` drop column `workflow_template_snapshot`;

drop table if exists `project__workflow_run_node`;

{{ if .Mysql }}drop index `project__workflow_run__correlation` on `project__workflow_run`{{ else }}drop index if exists `project__workflow_run__correlation`{{ end }};
alter table `project__workflow_run` drop column `reason`;
alter table `project__workflow_run` drop column `created`;
alter table `project__workflow_run` drop column `correlation_id`;
alter table `project__workflow_run` drop column `definition_snapshot`;
alter table `project__workflow_run` drop column `definition_revision`;
alter table `project__workflow_run` drop column `definition_version`;
alter table `project__workflow_run` drop column `actor_user_id`;
