alter table `task__runner_attempt` add column `docker_requested_image` varchar(512) not null default '';
alter table `task__runner_attempt` add column `docker_resolved_image` varchar(512) not null default '';
alter table `task__runner_attempt` add column `docker_policy_revision` int not null default 0;
alter table `task__runner_attempt` add column `docker_policy_hash` varchar(64) not null default '';
alter table `task__runner_attempt` add column `docker_nano_cpus` bigint not null default 0;
alter table `task__runner_attempt` add column `docker_memory_bytes` bigint not null default 0;
alter table `task__runner_attempt` add column `docker_pids_limit` bigint not null default 0;
