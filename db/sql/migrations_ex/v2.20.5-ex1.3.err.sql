drop table `task_group_dispatch_guard`;
alter table `task` drop column `task_group_keys`;
alter table `task` drop column `task_group_runner_ids`;
alter table `project__template` drop column `task_groups`;
drop table `project__task_group_grant`;
drop table `project__task_group`;
