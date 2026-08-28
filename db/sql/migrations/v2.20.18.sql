{{ if .Mysql }}
alter table `project__workflow_template` add column `parameter_definitions` longtext null;
update `project__workflow_template` set `parameter_definitions` = '[]' where `parameter_definitions` is null;
alter table `project__workflow_template` modify column `parameter_definitions` longtext not null;
alter table `project__workflow_node` add column `override_policy` longtext null;
update `project__workflow_node` set `override_policy` = '{}' where `override_policy` is null;
alter table `project__workflow_node` modify column `override_policy` longtext not null;
alter table `project__workflow_run` add column `parameter_snapshot` longtext null;
update `project__workflow_run` set `parameter_snapshot` = '{}' where `parameter_snapshot` is null;
alter table `project__workflow_run` modify column `parameter_snapshot` longtext not null;
alter table `project__workflow_run_node` add column `override_snapshot` longtext null;
update `project__workflow_run_node` set `override_snapshot` = '{}' where `override_snapshot` is null;
alter table `project__workflow_run_node` modify column `override_snapshot` longtext not null;
{{ else }}
alter table `project__workflow_template` add column `parameter_definitions` longtext not null default '[]';
alter table `project__workflow_node` add column `override_policy` longtext not null default '{}';
alter table `project__workflow_run` add column `parameter_snapshot` longtext not null default '{}';
alter table `project__workflow_run_node` add column `override_snapshot` longtext not null default '{}';
{{ end }}
