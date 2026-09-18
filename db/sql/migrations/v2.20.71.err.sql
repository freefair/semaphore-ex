alter table `task` drop column `ssh_keys`;
alter table `project__template` drop column `ssh_keys`;
alter table `project` drop column `always_ssh_keys`;
alter table `project` drop column `default_ssh_keys`;
