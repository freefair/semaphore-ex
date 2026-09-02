alter table `task` add column `execution_snapshot` longtext null;
alter table `project__workflow_run_node` add column `execution_snapshot` longtext not null default '';
