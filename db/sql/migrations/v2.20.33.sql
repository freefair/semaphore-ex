alter table `task__runner_attempt` add column `executor_type` varchar(16) not null default 'local';
alter table `task__runner_attempt` add column `container_id` varchar(128) not null default '';
alter table `task__runner_attempt` add column `container_name` varchar(128) not null default '';
