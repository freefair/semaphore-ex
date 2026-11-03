-- EX workflow permissions already use stable bits for view, edit, start and stop.
-- Approval previously depended on workflow policy rather than a project bit.
update `role` set `permissions` = `permissions` | 2097152;

-- Built-in roles are virtual immutable references; validate role IDs in the
-- application instead of referencing the upstream numeric role primary key.
create table `project__workflow_role` (
    `id` integer primary key autoincrement,
    `workflow_id` int not null,
    `role_id` varchar(64) not null,
    `project_id` int not null,
    `permissions` bigint not null default 0,
    foreign key (`workflow_id`) references project__workflow_template (`id`) on delete cascade,
    foreign key (`project_id`) references project (`id`) on delete cascade,
    unique (`workflow_id`, `role_id`)
);
create index `project__workflow_role__role_id` on `project__workflow_role` (`role_id`);
create index `project__workflow_role__project_id` on `project__workflow_role` (`project_id`);
