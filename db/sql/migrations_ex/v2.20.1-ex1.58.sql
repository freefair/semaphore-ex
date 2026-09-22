alter table `task` add column `execution_snapshot` longtext null;
{{ if .Mysql }}
alter table `project__workflow_run_node` add column `execution_snapshot` longtext null;
update `project__workflow_run_node` set `execution_snapshot`='' where `execution_snapshot` is null;
alter table `project__workflow_run_node` modify column `execution_snapshot` longtext not null;
{{ else }}
alter table `project__workflow_run_node` add column `execution_snapshot` longtext not null default '';
{{ end }}
