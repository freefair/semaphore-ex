-- Signed webhook state is separate from the historical optional Bearer
-- credential. Existing delivery configuration intentionally fails closed until
-- a signing key is created after this migration.
alter table `audit_webhook_config` add column `current_signing_secret_encrypted` longtext not null{{ if not .Mysql }} default ''{{ end }};
alter table `audit_webhook_config` add column `next_signing_secret_encrypted` longtext not null{{ if not .Mysql }} default ''{{ end }};
alter table `audit_webhook_config` add column `current_signing_key_id` varchar(64) not null{{ if not .Mysql }} default ''{{ end }};
alter table `audit_webhook_config` add column `next_signing_key_id` varchar(64) not null{{ if not .Mysql }} default ''{{ end }};
alter table `audit_webhook_config` add column `current_signing_generation` int not null{{ if not .Mysql }} default 0{{ end }};
alter table `audit_webhook_config` add column `next_signing_generation` int not null{{ if not .Mysql }} default 0{{ end }};
alter table `audit_webhook_config` add column `signing_state_revision` int not null{{ if not .Mysql }} default 0{{ end }};
{{ if .Mysql }}
alter table `audit_webhook_config` modify column `current_signing_key_id` varchar(64) not null default '';
alter table `audit_webhook_config` modify column `next_signing_key_id` varchar(64) not null default '';
alter table `audit_webhook_config` modify column `current_signing_generation` int not null default 0;
alter table `audit_webhook_config` modify column `next_signing_generation` int not null default 0;
alter table `audit_webhook_config` modify column `signing_state_revision` int not null default 0;
{{ end }}
alter table `audit_webhook_delivery` add column `last_signed_at` datetime null;
create table `audit_webhook_delivery_attempt` (
  `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
  `delivery_id` int not null,
  `event_id` varchar(128) not null,
  `attempt` int not null,
  `key_id` varchar(64) not null,
  `signed_at` datetime not null,
  `outcome` varchar(16) not null,
  `http_status` int null,
  `reason` varchar(32) not null default '',
  `created` datetime not null,
  `completed_at` datetime null,
  unique (`delivery_id`, `attempt`),
  foreign key (`delivery_id`) references `audit_webhook_delivery`(`id`) on delete cascade
);
create index `audit_webhook_delivery_attempt__delivery`
  on `audit_webhook_delivery_attempt`(`delivery_id`, `attempt`, `id`);

alter table `project__workflow_trigger` add column `current_signing_secret_encrypted` longtext not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_trigger` add column `next_signing_secret_encrypted` longtext not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_trigger` add column `current_signing_key_id` varchar(64) not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_trigger` add column `next_signing_key_id` varchar(64) not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_trigger` add column `current_signing_generation` int not null{{ if not .Mysql }} default 0{{ end }};
alter table `project__workflow_trigger` add column `next_signing_generation` int not null{{ if not .Mysql }} default 0{{ end }};
{{ if .Mysql }}
alter table `project__workflow_trigger` modify column `current_signing_key_id` varchar(64) not null default '';
alter table `project__workflow_trigger` modify column `next_signing_key_id` varchar(64) not null default '';
alter table `project__workflow_trigger` modify column `current_signing_generation` int not null default 0;
alter table `project__workflow_trigger` modify column `next_signing_generation` int not null default 0;
{{ end }}
alter table `project__workflow_trigger_invocation` add column `webhook_event_hash` varchar(64) null;
alter table `project__workflow_trigger_invocation` add column `webhook_event_id` varchar(128) not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_trigger_invocation` add column `webhook_key_id` varchar(64) not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_trigger_invocation` add column `webhook_signed_at` datetime null;
alter table `project__workflow_trigger_invocation` add column `webhook_replay_count` int not null{{ if not .Mysql }} default 0{{ end }};
alter table `project__workflow_trigger_invocation` add column `webhook_last_replayed_at` datetime null;
{{ if .Mysql }}
alter table `project__workflow_trigger_invocation` modify column `webhook_event_id` varchar(128) not null default '';
alter table `project__workflow_trigger_invocation` modify column `webhook_key_id` varchar(64) not null default '';
alter table `project__workflow_trigger_invocation` modify column `webhook_replay_count` int not null default 0;
{{ end }}
create unique index `project__workflow_trigger_invocation__webhook_event`
  on `project__workflow_trigger_invocation`(`workflow_trigger_id`, `webhook_event_hash`);
