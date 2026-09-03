-- Binary workflow artifacts use chunked SQL staging so every supported
-- database remains the durable authority. Only finalized metadata is visible.
create table `workflow_artifact_retention_policy` (
  `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
  `scope_key` varchar(96) not null,
  `scope` varchar(16) not null,
  `project_id` int null,
  `revision` int not null,
  `retention_seconds` bigint not null,
  `max_artifact_bytes` bigint not null,
  `max_run_bytes` bigint not null,
  `created_by_user_id` int not null,
  `created_at` datetime not null,
  unique (`scope_key`, `revision`),
  foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `workflow_artifact_retention_policy__scope`
  on `workflow_artifact_retention_policy`(`scope_key`, `revision`);

create table `workflow_file_artifact_run_usage` (
  `workflow_run_id` int primary key,
  `reserved_bytes` bigint not null,
  `artifact_count` int not null,
  `revision` int not null,
  foreign key (`workflow_run_id`) references `project__workflow_run`(`id`) on delete cascade
);

create table `workflow_file_artifact` (
  `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
  `project_id` int not null,
  `workflow_template_id` int not null,
  `workflow_run_id` int not null,
  `workflow_node_id` int not null,
  `workflow_definition_revision` int not null,
  `task_id` int not null,
  `attempt` int not null,
  `logical_name` varchar(128) not null,
  `filename` varchar(255) not null,
  `media_type` varchar(128) not null,
  `size_bytes` bigint not null,
  `uploaded_bytes` bigint not null,
  `sha256` varchar(64) not null,
  `state` varchar(16) not null,
  `revision` int not null,
  `producer_user_id` int not null,
  `producer_runner_id` int null,
  `producer_template_id` int not null,
  `producer_version` varchar(128) not null,
  `credential_provenance` longtext not null,
  `access_policy` longtext not null,
  `retention_global_revision` int not null,
  `retention_project_revision` int not null,
  `retention_seconds` bigint not null,
  `retention_max_artifact_bytes` bigint not null,
  `retention_max_run_bytes` bigint not null,
  `created_at` datetime not null,
  `finalized_at` datetime null,
  `expires_at` datetime null,
  `deleted_at` datetime null,
  unique (`workflow_run_id`, `workflow_node_id`, `task_id`, `attempt`, `logical_name`),
  foreign key (`project_id`) references `project`(`id`) on delete cascade,
  foreign key (`workflow_run_id`) references `project__workflow_run`(`id`) on delete cascade
);
create index `workflow_file_artifact__run_state`
  on `workflow_file_artifact`(`project_id`, `workflow_run_id`, `state`, `id`);
create index `workflow_file_artifact__retention`
  on `workflow_file_artifact`(`state`, `expires_at`, `id`);

create table `workflow_file_artifact_chunk` (
  `artifact_id` int not null,
  `ordinal` int not null,
  `offset_bytes` bigint not null,
  `size_bytes` int not null,
  `data` {{ if .Postgresql }}bytea{{ else }}longblob{{ end }} not null,
  primary key (`artifact_id`, `ordinal`),
  unique (`artifact_id`, `offset_bytes`),
  foreign key (`artifact_id`) references `workflow_file_artifact`(`id`) on delete cascade
);

create table `workflow_file_artifact_download_lease` (
  `lease_token` varchar(64) primary key,
  `artifact_id` int not null,
  `expires_at` datetime not null,
  `created_at` datetime not null,
  foreign key (`artifact_id`) references `workflow_file_artifact`(`id`)
);
create index `workflow_file_artifact_download_lease__artifact`
  on `workflow_file_artifact_download_lease`(`artifact_id`, `expires_at`);
