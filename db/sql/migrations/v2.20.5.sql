alter table `task` add `assignment_generation` integer not null default 0;
alter table `task` add `runner_assigned_at` datetime null;
alter table `task` add `recovery_reason` varchar(512) not null default '';

update `task`
set `assignment_generation`=1,
    `runner_assigned_at`=coalesce(`start`, `created`)
where `runner_id_snapshot` is not null;

create table `task__runner_attempt` (
    `id` integer primary key autoincrement,
    `project_id` integer not null,
    `task_id` integer not null,
    `generation` integer not null,
    `runner_id` integer not null,
    `runner_name` varchar(255) not null,
    `assigned_at` datetime not null,
    `ended_at` datetime null,
    `outcome` varchar(32) not null default 'active',
    `reason` varchar(512) not null default '',
    unique (`task_id`, `generation`),
    foreign key (`project_id`) references `project`(`id`) on delete cascade,
    foreign key (`task_id`) references `task`(`id`) on delete cascade
);

create index `task__runner_attempt_project_task` on `task__runner_attempt` (`project_id`, `task_id`);

insert into `task__runner_attempt`
    (`project_id`, `task_id`, `generation`, `runner_id`, `runner_name`, `assigned_at`, `ended_at`, `outcome`, `reason`)
select `project_id`, `id`, `assignment_generation`, `runner_id_snapshot`, coalesce(`runner_name`, ''),
       `runner_assigned_at`,
       case when `status` in ('success', 'error', 'stopped') then `end` else null end,
       case `status`
           when 'success' then 'succeeded'
           when 'error' then 'failed'
           when 'stopped' then 'stopped'
           else 'active'
       end,
       ''
from `task`
where `runner_id_snapshot` is not null;
