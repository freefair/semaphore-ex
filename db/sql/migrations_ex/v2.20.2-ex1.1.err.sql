{{ if .Sqlite }}
create table `project__workflow_delay_ex_65_rollback` (
  `id` integer primary key autoincrement,
  `project_id` int not null,
  `workflow_run_id` int not null,
  `workflow_node_id` int not null,
  `status` varchar(30) not null,
  `resume_at` datetime not null,
  `created` datetime not null,
  `resolved` datetime null,

  foreign key (`project_id`) references `project`(`id`) on delete cascade,
  foreign key (`workflow_run_id`) references `project__workflow_run`(`id`) on delete cascade,
  foreign key (`workflow_node_id`) references `project__workflow_node`(`id`) on delete cascade,
  unique (`workflow_run_id`, `workflow_node_id`)
);

insert into `project__workflow_delay_ex_65_rollback`
  (`id`, `project_id`, `workflow_run_id`, `workflow_node_id`, `status`, `resume_at`, `created`, `resolved`)
select `id`, `project_id`, `workflow_run_id`, `workflow_node_id`, `status`, `resume_at`, `created`, `resolved`
from `project__workflow_delay`;

drop table `project__workflow_delay`;
alter table `project__workflow_delay_ex_65_rollback` rename to `project__workflow_delay`;
create index `project__workflow_delay__workflow_run_id` on `project__workflow_delay`(`workflow_run_id`);
create index `project__workflow_delay__status_resume_at` on `project__workflow_delay`(`status`, `resume_at`);
{{ else }}
alter table `project__workflow_delay`
  add constraint `project__workflow_delay_workflow_node_id_fkey`
  foreign key (`workflow_node_id`) references `project__workflow_node`(`id`) on delete cascade;
{{ end }}
