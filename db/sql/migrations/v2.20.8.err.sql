{{if not .Sqlite}}
alter table `project__workflow_run` drop foreign key `project__workflow_run__revision_fk`;
alter table `project__workflow_edge` drop foreign key `project__workflow_edge__revision_fk`;
alter table `project__workflow_node` drop foreign key `project__workflow_node__revision_fk`;
{{end}}

drop index `project__workflow_run__revision_id`{{if .Mysql}} on `project__workflow_run`{{end}};
drop index `project__workflow_edge__revision_id`{{if .Mysql}} on `project__workflow_edge`{{end}};
drop index `project__workflow_node__revision_id`{{if .Mysql}} on `project__workflow_node`{{end}};

alter table `project__workflow_run` drop column `revision_id`;
alter table `project__workflow_edge` drop column `revision_id`;
alter table `project__workflow_node` drop column `revision_id`;

drop table `project__workflow_revision`;
