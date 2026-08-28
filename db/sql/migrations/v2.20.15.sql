alter table `project__workflow_run` add column `actor_user_id` int not null default 0;
alter table `project__workflow_run` add column `definition_version` int not null default 1;
alter table `project__workflow_run` add column `definition_revision` int not null default 1;
{{ if .Mysql }}
alter table `project__workflow_run` add column `definition_snapshot` longtext null;
update `project__workflow_run` set `definition_snapshot` = '{}' where `definition_snapshot` is null;
alter table `project__workflow_run` modify column `definition_snapshot` longtext not null;
{{ else }}
alter table `project__workflow_run` add column `definition_snapshot` longtext not null default '{}';
{{ end }}
alter table `project__workflow_run` add column `correlation_id` varchar(64) not null default '';
{{ if .Mysql }}
update `project__workflow_run` set `correlation_id` = concat('legacy-', `id`) where `correlation_id` = '';
{{ else }}
update `project__workflow_run` set `correlation_id` = 'legacy-' || `id` where `correlation_id` = '';
{{ end }}
{{ if .Sqlite }}
alter table `project__workflow_run` add column `created` datetime not null default '1970-01-01 00:00:00';
update `project__workflow_run` set `created` = coalesce(`start`, CURRENT_TIMESTAMP) where `created` = '1970-01-01 00:00:00';
{{ else }}
alter table `project__workflow_run` add column `created` datetime not null default CURRENT_TIMESTAMP;
{{ end }}
{{ if .Mysql }}
alter table `project__workflow_run` add column `reason` text null;
update `project__workflow_run` set `reason` = '' where `reason` is null;
alter table `project__workflow_run` modify column `reason` text not null;
{{ else }}
alter table `project__workflow_run` add column `reason` text not null default '';
{{ end }}
create unique index `project__workflow_run__correlation` on `project__workflow_run`(`project_id`, `workflow_template_id`, `correlation_id`);

create table `project__workflow_run_node` (
  `id` integer primary key autoincrement,
  `project_id` int not null,
  `workflow_run_id` int not null,
  `workflow_node_id` int not null,
  `template_id` int not null,
  `status` varchar(30) not null,
  `task_id` int null,
  `template_snapshot` longtext not null,
  `created` datetime not null,
  `queued` datetime null,
  `start` datetime null,
  `end` datetime null,
  `reason` text not null{{ if not .Mysql }} default ''{{ end }},

  foreign key (`project_id`) references `project`(`id`) on delete cascade,
  foreign key (`workflow_run_id`) references `project__workflow_run`(`id`) on delete cascade,
  foreign key (`task_id`) references `task`(`id`) on delete set null
);

create unique index `project__workflow_run_node__run_node` on `project__workflow_run_node`(`workflow_run_id`, `workflow_node_id`);
create index `project__workflow_run_node__project_run` on `project__workflow_run_node`(`project_id`, `workflow_run_id`);
create index `project__workflow_run_node__task_id` on `project__workflow_run_node`(`task_id`);

alter table `task` add column `workflow_template_snapshot` longtext null;
create unique index `task__workflow_run_node_unique` on `task`(`workflow_run_id`, `workflow_node_id`);
