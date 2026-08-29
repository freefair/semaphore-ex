create table `cluster__schedule_occurrence` (
  `occurrence_key` varchar(64) primary key,
  `schedule_id` int not null,
  `schedule_revision` varchar(128) not null,
  `intended_at` datetime not null,
  `owner_boot_id` varchar(64) not null,
  `fencing_token` bigint not null,
  `lease_expires_at` datetime not null,
  `task_id` int null,
  `completed_at` datetime null,
  `created` datetime not null,
  `updated` datetime not null
);

create index `cluster__schedule_occurrence__schedule_intended`
  on `cluster__schedule_occurrence`(`schedule_id`, `intended_at`);

alter table `task` add column `schedule_occurrence_key` varchar(64) null;
create unique index `task__schedule_occurrence_key` on `task`(`schedule_occurrence_key`);
