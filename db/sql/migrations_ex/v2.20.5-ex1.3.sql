create table `project__task_group` (
    `id` integer primary key autoincrement,
    `project_id` integer not null,
    `name` varchar(128) not null,
    `description` varchar(2048) not null default '',
    `max_parallel_tasks` integer not null default 1,
    `runner_ids` longtext null,
    `revision` integer not null default 1,
    foreign key (`project_id`) references `project` (`id`) on delete restrict,
    unique (`project_id`, `name`)
);
create table `project__task_group_grant` (
    `group_id` integer not null,
    `project_id` integer not null,
    primary key (`group_id`, `project_id`),
    foreign key (`group_id`) references `project__task_group` (`id`) on delete cascade,
    foreign key (`project_id`) references `project` (`id`) on delete cascade
);
alter table `project__template` add column `task_groups` longtext null;
alter table `task` add column `task_group_keys` longtext null;
alter table `task` add column `task_group_runner_ids` longtext null;
create table `task_group_dispatch_guard` (`id` integer primary key);
insert into `task_group_dispatch_guard` (`id`) values (1);
