alter table `project__workflow_template` add column `max_parallel_tasks` int not null default 4;
alter table `project__workflow_node` add column `join_mode` varchar(30) not null default 'all-successful';
{{ if .Mysql }}
alter table `project__workflow_edge` add column `condition_expression` text null;
update `project__workflow_edge` set `condition_expression` = '' where `condition_expression` is null;
alter table `project__workflow_edge` modify column `condition_expression` text not null;
alter table `project__workflow_edge` add column `condition_program` longtext null;
update `project__workflow_edge` set `condition_program` = '{}' where `condition_program` is null;
alter table `project__workflow_edge` modify column `condition_program` longtext not null;
alter table `project__workflow_run_node` add column `result` longtext null;
update `project__workflow_run_node` set `result` = '{}' where `result` is null;
alter table `project__workflow_run_node` modify column `result` longtext not null;
{{ else }}
alter table `project__workflow_edge` add column `condition_expression` text not null default '';
alter table `project__workflow_edge` add column `condition_program` longtext not null default '{}';
alter table `project__workflow_run_node` add column `result` longtext not null default '{}';
{{ end }}
