drop index if exists `task__workflow_run_node_unique`;
alter table `task` drop column `workflow_template_snapshot`;

drop index if exists `project__workflow_run_node__task_id`;
drop index if exists `project__workflow_run_node__project_run`;
drop index if exists `project__workflow_run_node__run_node`;
drop table if exists `project__workflow_run_node`;

drop index if exists `project__workflow_run__correlation`;
alter table `project__workflow_run` drop column `reason`;
alter table `project__workflow_run` drop column `created`;
alter table `project__workflow_run` drop column `correlation_id`;
alter table `project__workflow_run` drop column `definition_snapshot`;
alter table `project__workflow_run` drop column `definition_revision`;
alter table `project__workflow_run` drop column `definition_version`;
alter table `project__workflow_run` drop column `actor_user_id`;
