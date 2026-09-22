alter table `project__template` add `runner_tags` text null;
alter table `project__template` add `runner_tag_match_mode` varchar(16) not null default 'all';
alter table `task` add `placement_decision` text null;
alter table `task__runner_attempt` add `requested_tags` text null;
alter table `task__runner_attempt` add `match_mode` varchar(16) not null default 'all';
alter table `task__runner_attempt` add `placement_reason` varchar(512) not null default '';
