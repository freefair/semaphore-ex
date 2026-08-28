alter table `project__workflow_run_node` drop column `result`;
alter table `project__workflow_edge` drop column `condition_program`;
alter table `project__workflow_edge` drop column `condition_expression`;
alter table `project__workflow_node` drop column `join_mode`;
alter table `project__workflow_template` drop column `max_parallel_tasks`;
