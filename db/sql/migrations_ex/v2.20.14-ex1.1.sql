-- Preserve the published upstream 2.20.14 bytes. Workflow targets are an EX
-- compatibility extension anchored immediately after that upstream migration.
{{if .Sqlite}}
create table project__integration_new_2_20_14_ex_1_1 (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           VARCHAR(255) NOT NULL,
    project_id     INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    template_id    INTEGER NULL REFERENCES project__template(id) ON DELETE CASCADE,
    workflow_id    INTEGER NULL REFERENCES project__workflow_template(id) ON DELETE CASCADE,
	workflow_user_id INTEGER NULL REFERENCES user(id) ON DELETE SET NULL,
    auth_method    VARCHAR(15) NOT NULL DEFAULT 'none',
    auth_secret_id INTEGER REFERENCES access_key(id) ON DELETE SET NULL,
    auth_header    VARCHAR(255) NULL,
    searchable     INTEGER NOT NULL DEFAULT 0,
    task_params_id INTEGER REFERENCES project__task_params(id)
);

insert into project__integration_new_2_20_14_ex_1_1 (id, name, project_id, template_id, auth_method, auth_secret_id, auth_header, searchable, task_params_id)
    select id, name, project_id, template_id, auth_method, auth_secret_id, auth_header, searchable, task_params_id from project__integration;

drop table project__integration;
alter table project__integration_new_2_20_14_ex_1_1 rename to project__integration;
create index project__integration__auth_secret_id on project__integration(auth_secret_id);
create index project__integration__project_id on project__integration(project_id);
create index project__integration__template_id on project__integration(template_id);
create index project__integration__workflow_id on project__integration(workflow_id);
create index project__integration__workflow_user_id on project__integration(workflow_user_id);
alter table project__workflow_run add column integration_id int null references project__integration(id) on delete set null;
create index project__workflow_run__integration_id on project__workflow_run(integration_id);
{{else}}
{{if .Postgresql}}
alter table `project__integration` alter column `template_id` drop not null;
{{else}}
alter table `project__integration` change `template_id` `template_id` int;
{{end}}
alter table `project__integration` add `workflow_id` int null;
alter table `project__integration` add `workflow_user_id` int null;
alter table `project__integration` add constraint `project__integration__workflow_fk` foreign key (`workflow_id`) references `project__workflow_template`(`id`) on delete cascade;
alter table `project__integration` add constraint `project__integration__workflow_user_fk` foreign key (`workflow_user_id`) references `user`(`id`) on delete set null;
create index `project__integration__workflow_id` on `project__integration`(`workflow_id`);
create index `project__integration__workflow_user_id` on `project__integration`(`workflow_user_id`);
alter table `project__workflow_run` add `integration_id` int null;
alter table `project__workflow_run` add constraint `project__workflow_run__integration_fk` foreign key (`integration_id`) references `project__integration`(`id`) on delete set null;
create index `project__workflow_run__integration_id` on `project__workflow_run`(`integration_id`);
{{end}}
