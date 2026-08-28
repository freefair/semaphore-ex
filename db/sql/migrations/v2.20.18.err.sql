alter table `project__workflow_run_node` drop column `override_snapshot`;
alter table `project__workflow_run` drop column `parameter_snapshot`;
alter table `project__workflow_node` drop column `override_policy`;
alter table `project__workflow_template` drop column `parameter_definitions`;
