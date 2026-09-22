create table `docker_execution_policy` (
  `singleton_id` integer primary key,
  `revision` int not null,
  `policy_hash` varchar(64) not null,
  `policy_json` longtext not null
);

alter table `runner` add column `docker_policy_revision` int not null default 0;
alter table `runner` add column `docker_policy_hash` varchar(64) not null default '';
