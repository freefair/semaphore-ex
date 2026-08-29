create table `project__workflow_trigger` (
  `id` integer primary key autoincrement,
  `project_id` int not null,
  `workflow_template_id` int not null,
  `revision` int not null,
  `name` varchar(128) not null,
  `type` varchar(20) not null,
  `owner_user_id` int not null,
  `enabled` int not null default 1,
  `cron_format` varchar(250) not null default '',
  `input_mappings` longtext not null,
  `credential_hash` varchar(71) not null default '',
  `credential_generation` int not null default 0,
  `created` datetime not null,
  `updated` datetime not null,
  `last_fired` datetime null,
  `last_result` varchar(30) not null default '',

  foreign key (`project_id`) references `project`(`id`) on delete cascade,
  foreign key (`workflow_template_id`) references `project__workflow_template`(`id`) on delete cascade
);

create index `project__workflow_trigger__workflow`
  on `project__workflow_trigger`(`project_id`, `workflow_template_id`, `id`);
create index `project__workflow_trigger__schedule`
  on `project__workflow_trigger`(`type`, `enabled`, `id`);

create table `project__workflow_trigger_invocation` (
  `id` integer primary key autoincrement,
  `project_id` int not null,
  `workflow_trigger_id` int not null,
  `workflow_template_id` int not null,
  `trigger_revision` int not null,
  `credential_generation` int not null default 0,
  `definition_revision` int not null,
  `request_key_hash` varchar(71) null,
  `occurrence_identity` varchar(64) null,
  `status` varchar(30) not null,
  `run_id` int null,
  `actor_user_id` int not null default 0,
  `trigger_snapshot` longtext not null,
  `input_snapshot` longtext not null,
  `result` varchar(30) not null default '',
  `reason` text not null{{ if not .Mysql }} default ''{{ end }},
  `created` datetime not null,
  `updated` datetime not null,
  `expires_at` datetime null,

  foreign key (`project_id`) references `project`(`id`) on delete cascade,
  foreign key (`workflow_trigger_id`) references `project__workflow_trigger`(`id`) on delete cascade,
  foreign key (`workflow_template_id`) references `project__workflow_template`(`id`) on delete cascade,
  foreign key (`run_id`) references `project__workflow_run`(`id`) on delete set null
);

create unique index `project__workflow_trigger_invocation__external_request`
  on `project__workflow_trigger_invocation`(`workflow_trigger_id`, `credential_generation`, `request_key_hash`);
create unique index `project__workflow_trigger_invocation__scheduled_occurrence`
  on `project__workflow_trigger_invocation`(`occurrence_identity`);
create index `project__workflow_trigger_invocation__history`
  on `project__workflow_trigger_invocation`(`project_id`, `workflow_trigger_id`, `id`);
create index `project__workflow_trigger_invocation__expiry`
  on `project__workflow_trigger_invocation`(`expires_at`, `id`);

{{ if .Mysql }}
alter table `project__workflow_run` add column `trigger_snapshot` longtext null;
update `project__workflow_run` set `trigger_snapshot` = '{}' where `trigger_snapshot` is null;
alter table `project__workflow_run` modify column `trigger_snapshot` longtext not null;
{{ else }}
alter table `project__workflow_run` add column `trigger_snapshot` longtext not null default '{}';
{{ end }}
