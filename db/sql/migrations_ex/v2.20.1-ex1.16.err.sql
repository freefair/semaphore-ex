drop table if exists `project__workflow_artifact`;

alter table `project__workflow_run_node` drop column `artifact_inputs`;
alter table `project__workflow_node` drop column `artifact_inputs`;
alter table `project__workflow_node` drop column `artifact_outputs`;
