alter table `task__runner_attempt` drop `resolved_executor_image`;
alter table `task__runner_attempt` drop `requested_executor_image`;
alter table `task` drop `resolved_executor_image`;
alter table `task` drop `requested_executor_image`;
alter table `runner` drop `executor_type`;
