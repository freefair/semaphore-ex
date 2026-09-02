create table `project__deployment_window_policy` (
 `project_id` int primary key, `revision` int not null,
 `timezone` varchar(128) not null, `default_decision` varchar(8) not null,
 `created` datetime not null, `updated` datetime not null,
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create table `project__deployment_window_rule` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `project_id` int not null, `policy_revision` int not null,
 `name` varchar(255) not null, `active` tinyint not null,
 `kind` varchar(8) not null, `scope` varchar(16) not null,
 `template_id` int null, `workflow_template_id` int null,
 `recurrence` varchar(255) not null, `duration_minutes` int not null,
 `effective_from` varchar(10) null, `effective_until` varchar(10) null,
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `project__deployment_window_rule__policy`
 on `project__deployment_window_rule` (`project_id`, `policy_revision`, `id`);
create table `project__deployment_window_decision` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `project_id` int not null, `decision_key` varchar(128) not null,
 `source` varchar(16) not null, `origin` varchar(24) not null,
 `template_id` int null, `workflow_template_id` int null, `schedule_id` int null,
 `task_id` int null, `workflow_run_id` int null, `workflow_run_node_id` int null,
 `actor_user_id` int null, `policy_revision` int not null,
 `effective_timezone` varchar(128) not null, `evaluated_at` datetime not null,
 `state` varchar(16) not null, `reason` varchar(32) not null,
 `next_eligible_at` datetime null, `next_eligible_known` tinyint not null,
 `override_actor_user_id` int null, `override_category` varchar(32) null,
 `override_reference` varchar(128) null, `matched_rules` longtext not null,
 `created` datetime not null,
 unique (`project_id`, `source`, `origin`, `decision_key`),
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `project__deployment_window_decision__project_created`
 on `project__deployment_window_decision` (`project_id`, `created`, `id`);
