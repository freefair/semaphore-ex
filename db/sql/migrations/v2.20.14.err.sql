alter table `project__workflow_edge` drop column `label`;
alter table `project__workflow_node` drop column `display_name`;
alter table `project__workflow_template` drop column `revision`;
alter table `project__workflow_template` drop column `definition_version`;
