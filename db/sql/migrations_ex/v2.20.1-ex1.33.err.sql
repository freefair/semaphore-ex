alter table `runner` drop column `docker_policy_hash`;
alter table `runner` drop column `docker_policy_revision`;
drop table `docker_execution_policy`;
