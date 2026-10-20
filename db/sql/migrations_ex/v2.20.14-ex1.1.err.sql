-- Workflow-target integrations have no template and cannot survive rollback
-- to the upstream-only schema.
{{if .Sqlite}}
delete from project__integration_alias where integration_id in (select id from project__integration where template_id is null);
delete from project__integration_matcher where integration_id in (select id from project__integration where template_id is null);
delete from project__integration_extract_value where integration_id in (select id from project__integration where template_id is null);
update task set integration_id = null where integration_id in (select id from project__integration where template_id is null);
{{end}}
delete from `project__integration` where `template_id` is null;

{{if .Mysql}}alter table `project__workflow_run` drop foreign key `project__workflow_run__integration_fk`;{{end}}
{{if .Postgresql}}alter table `project__workflow_run` drop constraint `project__workflow_run__integration_fk`;{{end}}
{{ if .Mysql }}drop index `project__workflow_run__integration_id` on `project__workflow_run`{{ else }}drop index if exists `project__workflow_run__integration_id`{{ end }};
alter table `project__workflow_run` drop column `integration_id`;

{{if .Sqlite}}
create table project__integration_old_2_20_14_ex_1_1 (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           VARCHAR(255) NOT NULL,
    project_id     INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    template_id    INTEGER NOT NULL REFERENCES project__template(id) ON DELETE CASCADE,
    auth_method    VARCHAR(15) NOT NULL DEFAULT 'none',
    auth_secret_id INTEGER REFERENCES access_key(id) ON DELETE SET NULL,
    auth_header    VARCHAR(255) NULL,
    searchable     INTEGER NOT NULL DEFAULT 0,
    task_params_id INTEGER REFERENCES project__task_params(id)
);

insert into project__integration_old_2_20_14_ex_1_1 (id, name, project_id, template_id, auth_method, auth_secret_id, auth_header, searchable, task_params_id)
    select id, name, project_id, template_id, auth_method, auth_secret_id, auth_header, searchable, task_params_id from project__integration;

drop table project__integration;
alter table project__integration_old_2_20_14_ex_1_1 rename to project__integration;
create index project__integration__auth_secret_id on project__integration(auth_secret_id);
create index project__integration__project_id on project__integration(project_id);
create index project__integration__template_id on project__integration(template_id);
{{else}}
{{if .Postgresql}}
alter table `project__integration` drop constraint `project__integration__workflow_fk`;
alter table `project__integration` drop constraint `project__integration__workflow_user_fk`;
{{else}}
alter table `project__integration` drop foreign key `project__integration__workflow_fk`;
alter table `project__integration` drop foreign key `project__integration__workflow_user_fk`;
{{end}}
alter table `project__integration` drop column `workflow_user_id`;
alter table `project__integration` drop column `workflow_id`;
{{if .Postgresql}}
alter table `project__integration` alter column `template_id` set not null;
{{else}}
alter table `project__integration` change `template_id` `template_id` int not null;
{{end}}
{{end}}
