alter table `task__runner_attempt` drop column `docker_pids_limit`;
alter table `task__runner_attempt` drop column `docker_memory_bytes`;
alter table `task__runner_attempt` drop column `docker_nano_cpus`;
alter table `task__runner_attempt` drop column `docker_policy_hash`;
alter table `task__runner_attempt` drop column `docker_policy_revision`;
alter table `task__runner_attempt` drop column `docker_resolved_image`;
alter table `task__runner_attempt` drop column `docker_requested_image`;
