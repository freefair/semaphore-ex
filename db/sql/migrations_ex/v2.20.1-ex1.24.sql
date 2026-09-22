create table `cluster__task_control` (
  `task_id` int primary key,
  `owner_boot_id` varchar(64) not null,
  `fencing_token` bigint not null,
  `lease_expires_at` datetime not null,
  `runner_id` int not null,
  `assignment_generation` int not null,
  `execution_stable_id` varchar(256) not null,
  `last_observed_at` datetime null,
  `created` datetime not null,
  `updated` datetime not null
);

create index `cluster__task_control__lease` on `cluster__task_control`(`lease_expires_at`);
