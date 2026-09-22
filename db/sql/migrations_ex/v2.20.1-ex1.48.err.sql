{{ if .Mysql }}drop index `project__workflow_approval_contribution__actor` on `project__workflow_approval_contribution`{{ else }}drop index `project__workflow_approval_contribution__actor`{{ end }};
drop table `project__workflow_approval_contribution`;
alter table `project__workflow_approval` drop column `role_policy_revision`;
alter table `project__workflow_approval` drop column `role_policy_snapshot`;
alter table `project__workflow_node` drop column `approval_role_policy_revision`;
alter table `project__workflow_node` drop column `approval_role_policy`;
alter table `project__workflow_template` drop column `access_policy_revision`;
alter table `project__workflow_template` drop column `access_policy`;
update `role` set `permissions` = `permissions` & ~ (32 | 64 | 128 | 256 | 512)
 where `project_id` is not null;
