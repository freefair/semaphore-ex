alter table `cluster__schedule_occurrence` add column `terminal_outcome` varchar(16) not null default 'pending';
alter table `cluster__schedule_occurrence` add column `deployment_window_decision_id` int null;
alter table `cluster__schedule_occurrence` add column `next_eligible_at` datetime null;
alter table `cluster__schedule_occurrence` add column `next_eligible_known` tinyint not null default 0;
alter table `cluster__schedule_occurrence` add column `blocked_at` datetime null;
create index `cluster__schedule_occurrence__decision` on `cluster__schedule_occurrence`(`deployment_window_decision_id`);
update `cluster__schedule_occurrence` set `terminal_outcome`='completed' where `completed_at` is not null;

alter table `project__workflow_trigger_invocation` add column `deployment_window_decision_id` int null;
alter table `project__workflow_trigger_invocation` add column `next_eligible_at` datetime null;
alter table `project__workflow_trigger_invocation` add column `next_eligible_known` tinyint not null default 0;
alter table `project__workflow_trigger_invocation` add column `blocked_at` datetime null;
create index `project__workflow_trigger_invocation__decision` on `project__workflow_trigger_invocation`(`deployment_window_decision_id`);

alter table `project__workflow_run_node` add column `deployment_window_decision_id` int null;
alter table `project__workflow_run_node` add column `next_eligible_at` datetime null;
alter table `project__workflow_run_node` add column `next_eligible_known` tinyint not null default 0;
alter table `project__workflow_run_node` add column `blocked_at` datetime null;
create index `project__workflow_run_node__deployment_window_decision` on `project__workflow_run_node`(`deployment_window_decision_id`);
