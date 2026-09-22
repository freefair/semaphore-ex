create table `notification_destination` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `project_id` int null, `name` varchar(128) not null, `provider` varchar(64) not null,
 `environment` varchar(64) not null default '', `encrypted_credential` longtext not null,
 `credential_configured` integer not null default 0, `enabled` integer not null default 1,
 `paused` integer not null default 0, `revision` int not null, `created` datetime not null,
 `updated` datetime not null,
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `notification_destination__scope`
 on `notification_destination` (`project_id`, `id`);
create table `notification_rule` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `project_id` int null, `destination_id` int not null, `source_kinds` varchar(256) not null,
 `lifecycle_actions` varchar(256) not null, `minimum_severity` varchar(16) not null,
 `enabled` integer not null default 1, `revision` int not null, `created` datetime not null,
 `updated` datetime not null,
 foreign key (`project_id`) references `project`(`id`) on delete cascade,
 foreign key (`destination_id`) references `notification_destination`(`id`) on delete restrict
);
create index `notification_rule__scope`
 on `notification_rule` (`project_id`, `enabled`, `id`);
create table `notification_event` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `schema_version` varchar(64) not null, `event_id` varchar(32) not null, `source_event_key` varchar(64) not null, `source_revision` int not null,
 `project_id` int null, `source_kind` varchar(32) not null,
 `source_id` varchar(128) not null, `lifecycle_id` varchar(128) not null, `severity` varchar(16) not null,
 `lifecycle_action` varchar(16) not null, `incident_key` varchar(64) not null,
 `details` longtext not null, `routing_outcome` varchar(16) not null, `occurred_at` datetime not null, `created` datetime not null,
 unique (`event_id`), unique (`source_event_key`)
);
create index `notification_event__scope_history`
 on `notification_event` (`project_id`, `id`);
create index `notification_event__incident`
 on `notification_event` (`incident_key`, `id`);
create table `notification_delivery` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `notification_event_id` int not null, `destination_id` int not null,
 `destination_revision` int not null, `destination_name` varchar(128) not null,
 `destination_provider` varchar(64) not null, `destination_environment` varchar(64) not null,
 `incident_key` varchar(64) not null,
 `idempotency_key` varchar(64) not null, `status` varchar(16) not null,
 `attempts` int not null default 0, `next_attempt` datetime not null,
 `lease_token` varchar(32) not null default '', `lease_until` datetime null,
 `last_reason` varchar(32) not null default '', `created` datetime not null,
 `updated` datetime not null, `delivered_at` datetime null,
 unique (`notification_event_id`, `destination_id`),
 unique (`idempotency_key`),
 foreign key (`notification_event_id`) references `notification_event`(`id`) on delete restrict
);
create index `notification_delivery__due`
 on `notification_delivery` (`status`, `next_attempt`, `id`);
alter table `task` add column `notification_revision` int not null default 0;
alter table `project__workflow_run` add column `notification_revision` int not null default 0;
alter table `project__workflow_approval` add column `notification_revision` int not null default 0;
