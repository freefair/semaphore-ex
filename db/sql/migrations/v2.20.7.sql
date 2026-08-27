alter table `runner` add `executor_type` varchar(16) not null default 'local';
alter table `task` add `requested_executor_image` varchar(255) null;
alter table `task` add `resolved_executor_image` varchar(255) null;
alter table `task__runner_attempt` add `requested_executor_image` varchar(255) null;
alter table `task__runner_attempt` add `resolved_executor_image` varchar(255) null;
