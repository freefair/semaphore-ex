alter table `task__runner_attempt` drop `placement_reason`;
alter table `task__runner_attempt` drop `match_mode`;
alter table `task__runner_attempt` drop `requested_tags`;
alter table `task` drop `placement_decision`;
alter table `project__template` drop `runner_tag_match_mode`;
alter table `project__template` drop `runner_tags`;
