{{ if .Mysql }}drop index `project__workflow_approval__pending_deadline` on `project__workflow_approval`{{ else }}drop index if exists `project__workflow_approval__pending_deadline`{{ end }};

alter table `project__workflow_approval` drop column `correlation_id`;
alter table `project__workflow_approval` drop column `decision_source`;
alter table `project__workflow_approval` drop column `decision_comment`;
alter table `project__workflow_approval` drop column `timeout_outcome`;
alter table `project__workflow_approval` drop column `request_actor_user_id`;
alter table `project__workflow_approval` drop column `separation_of_duties`;
alter table `project__workflow_approval` drop column `eligible_permission`;
alter table `project__workflow_approval` drop column `prompt`;
alter table `project__workflow_approval` drop column `deadline`;

alter table `project__workflow_node` drop column `approval_separation_of_duties`;
alter table `project__workflow_node` drop column `approval_timeout_outcome`;
alter table `project__workflow_node` drop column `approval_permission`;
