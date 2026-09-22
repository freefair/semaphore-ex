{{ if .Mysql }}drop index `project__workflow_trigger_invocation__webhook_event` on `project__workflow_trigger_invocation`{{ else }}drop index `project__workflow_trigger_invocation__webhook_event`{{ end }};
alter table `project__workflow_trigger_invocation` drop column `webhook_last_replayed_at`;
alter table `project__workflow_trigger_invocation` drop column `webhook_replay_count`;
alter table `project__workflow_trigger_invocation` drop column `webhook_signed_at`;
alter table `project__workflow_trigger_invocation` drop column `webhook_key_id`;
alter table `project__workflow_trigger_invocation` drop column `webhook_event_id`;
alter table `project__workflow_trigger_invocation` drop column `webhook_event_hash`;

alter table `project__workflow_trigger` drop column `next_signing_key_id`;
alter table `project__workflow_trigger` drop column `current_signing_key_id`;
alter table `project__workflow_trigger` drop column `next_signing_generation`;
alter table `project__workflow_trigger` drop column `current_signing_generation`;
alter table `project__workflow_trigger` drop column `next_signing_secret_encrypted`;
alter table `project__workflow_trigger` drop column `current_signing_secret_encrypted`;

drop table `audit_webhook_delivery_attempt`;
alter table `audit_webhook_delivery` drop column `last_signed_at`;

alter table `audit_webhook_config` drop column `signing_state_revision`;
alter table `audit_webhook_config` drop column `next_signing_generation`;
alter table `audit_webhook_config` drop column `current_signing_generation`;
alter table `audit_webhook_config` drop column `next_signing_key_id`;
alter table `audit_webhook_config` drop column `current_signing_key_id`;
alter table `audit_webhook_config` drop column `next_signing_secret_encrypted`;
alter table `audit_webhook_config` drop column `current_signing_secret_encrypted`;
