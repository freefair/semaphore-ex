alter table `project__workflow_approval` drop column `notification_revision`;
alter table `project__workflow_run` drop column `notification_revision`;
alter table `task` drop column `notification_revision`;
drop table `notification_delivery`;
drop table `notification_event`;
drop table `notification_rule`;
drop table `notification_destination`;
