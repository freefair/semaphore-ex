alter table `runner` add column `inventory_refresh_version` int not null default 0;

create table `inventory__snapshot` (
 `task_id` int not null,
 `generation` int not null,
 `project_id` int not null,
 `inventory_id` int not null,
 `inventory_name` varchar(255) not null,
 `template_id` int not null,
 `state` varchar(16) not null,
 `host_count` int not null default 0,
 `received_hosts` int not null default 0,
 `created` datetime not null,
 primary key (`task_id`, `generation`),
 foreign key (`task_id`) references `task` (`id`) on delete cascade
);
create index `inventory__snapshot__latest` on `inventory__snapshot` (`project_id`, `inventory_id`, `state`, `task_id`, `generation`);

create table `inventory__snapshot_host` (
 `task_id` int not null,
 `generation` int not null,
 `host` varchar(255) not null,
 `groups_json` text not null,
 primary key (`task_id`, `generation`, `host`),
 foreign key (`task_id`, `generation`) references `inventory__snapshot` (`task_id`, `generation`) on delete cascade
);
create index `inventory__snapshot_host__host` on `inventory__snapshot_host` (`host`, `task_id`);
