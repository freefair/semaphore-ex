create table `policy_guardrail_draft` (
 `scope_key` varchar(64) primary key, `scope` varchar(16) not null,
 `project_id` int null, `source_yaml` longtext not null,
 `revision` int not null, `active_revision` int null,
 `updated_by` int not null, `created` datetime not null, `updated` datetime not null,
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `policy_guardrail_draft__project` on `policy_guardrail_draft` (`project_id`);

create table `policy_guardrail_revision` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `scope_key` varchar(64) not null, `scope` varchar(16) not null,
 `project_id` int null, `revision` int not null, `parent_revision` int null,
 `rollback_of_revision` int null, `rollback_reason` varchar(512) not null default '',
 `source_yaml` longtext not null, `compiled_json` longtext not null,
 `fingerprint` varchar(71) not null, `compiler_version` int not null,
 `published_by` int not null, `created` datetime not null,
 unique (`scope_key`, `revision`),
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `policy_guardrail_revision__scope` on `policy_guardrail_revision` (`scope_key`, `revision`);

create table `policy_guardrail_evaluation` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `project_id` int not null, `decision_key` varchar(128) not null,
 `intent` varchar(16) not null, `source` varchar(32) not null,
 `template_id` int null, `workflow_template_id` int null,
 `workflow_run_id` int null, `workflow_run_node_id` int null, `task_id` int null,
 `actor_user_id` int null, `input_fingerprint` varchar(71) not null,
 `revisions_json` longtext not null, `findings_json` longtext not null,
 `decision` varchar(8) not null, `evaluated_at` datetime not null, `created` datetime not null,
 unique (`project_id`, `source`, `decision_key`),
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `policy_guardrail_evaluation__project_created`
 on `policy_guardrail_evaluation` (`project_id`, `created`, `id`);
