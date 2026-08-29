alter table `project__workflow_run` drop column `trigger_snapshot`;

drop table if exists `project__workflow_trigger_invocation`;
drop table if exists `project__workflow_trigger`;
