alter table `project` add column `default_ssh_keys` longtext null;
alter table `project` add column `always_ssh_keys` longtext null;
alter table `project__template` add column `ssh_keys` longtext null;
alter table `task` add column `ssh_keys` longtext null;
