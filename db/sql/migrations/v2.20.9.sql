create table `task__summary` (
    `task_id` integer not null primary key,
    `project_id` integer not null,
    `schema_version` integer not null,
    `runner_result_version` integer not null default 0,
    `state` varchar(16) not null default 'collecting',
    `task_status` varchar(16) not null default '',
    `expected_hosts` integer not null default 0,
    `diagnostic` varchar(512) not null default '',
    `started_at` datetime null,
    `ended_at` datetime null,
    `updated` datetime not null,
    unique (`project_id`, `task_id`),
    foreign key (`project_id`) references `project`(`id`) on delete cascade,
    foreign key (`task_id`) references `task`(`id`) on delete cascade
);

create table `task__summary_event` (
    `id` integer primary key autoincrement,
    `project_id` integer not null,
    `task_id` integer not null,
    `schema_version` integer not null,
    `event_id` varchar(255) not null,
    `kind` varchar(32) not null,
    `play_id` varchar(255) not null default '',
    `stage_id` varchar(255) not null default '',
    `stage` varchar(255) not null default '',
    `host` varchar(255) not null default '',
    `status` varchar(32) not null default '',
    `changed` integer not null default 0,
    `failed` integer not null default 0,
    `ignored` integer not null default 0,
    `ok` integer not null default 0,
    `rescued` integer not null default 0,
    `skipped` integer not null default 0,
    `unreachable` integer not null default 0,
    `expected_hosts` integer not null default 0,
    `started_at` datetime null,
    `ended_at` datetime null,
    `duration_ms` bigint not null default 0,
    `error` text not null,
    `output_time` datetime null,
    `created` datetime not null,
    unique (`task_id`, `event_id`),
    foreign key (`project_id`) references `project`(`id`) on delete cascade,
    foreign key (`task_id`) references `task`(`id`) on delete cascade
);

create index `task__summary_event_project_task_kind_id`
    on `task__summary_event` (`project_id`, `task_id`, `kind`, `id`);

create index `task__summary_event_project_task_host`
    on `task__summary_event` (`project_id`, `task_id`, `host`);
