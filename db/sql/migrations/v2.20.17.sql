{{ if .Mysql }}
alter table `project__workflow_node` add column `artifact_outputs` longtext null;
update `project__workflow_node` set `artifact_outputs` = '[]' where `artifact_outputs` is null;
alter table `project__workflow_node` modify column `artifact_outputs` longtext not null;
alter table `project__workflow_node` add column `artifact_inputs` longtext null;
update `project__workflow_node` set `artifact_inputs` = '[]' where `artifact_inputs` is null;
alter table `project__workflow_node` modify column `artifact_inputs` longtext not null;
alter table `project__workflow_run_node` add column `artifact_inputs` longtext null;
update `project__workflow_run_node` set `artifact_inputs` = '[]' where `artifact_inputs` is null;
alter table `project__workflow_run_node` modify column `artifact_inputs` longtext not null;
{{ else }}
alter table `project__workflow_node` add column `artifact_outputs` longtext not null default '[]';
alter table `project__workflow_node` add column `artifact_inputs` longtext not null default '[]';
alter table `project__workflow_run_node` add column `artifact_inputs` longtext not null default '[]';
{{ end }}

create table `project__workflow_artifact` (
  `id` integer primary key autoincrement,
  `project_id` int not null,
  `workflow_run_id` int not null,
  `workflow_node_id` int not null,
  `task_id` int not null,
  `attempt` int not null,
  `name` varchar(64) not null,
  `schema` longtext not null,
  `sensitive` int not null default 0,
  `availability` varchar(20) not null,
  `size_bytes` int not null default 0,
  `reference_fingerprint` varchar(80) not null,
  `diagnostic` text not null{{ if not .Mysql }} default ''{{ end }},
  `value_json` longtext null,
  `encrypted_value` longtext null,

  foreign key (`project_id`) references `project`(`id`) on delete cascade,
  foreign key (`workflow_run_id`) references `project__workflow_run`(`id`) on delete cascade,
  foreign key (`task_id`) references `task`(`id`) on delete cascade
);

create unique index `project__workflow_artifact__attempt_name` on `project__workflow_artifact`(`workflow_run_id`, `task_id`, `attempt`, `name`);
create index `project__workflow_artifact__run_node` on `project__workflow_artifact`(`project_id`, `workflow_run_id`, `workflow_node_id`);
