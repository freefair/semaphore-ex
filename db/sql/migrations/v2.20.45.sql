create table `kubernetes_execution_policy` (
  `cluster_alias` varchar(128) primary key,
  `revision` int not null,
  `policy_hash` varchar(64) not null,
  `policy_json` longtext not null
);

alter table `runner` add column `k8s_cluster_alias` varchar(128) not null default '';
alter table `runner` add column `k8s_policy_revision` int not null default 0;
alter table `runner` add column `k8s_policy_hash` varchar(64) not null default '';
alter table `task__runner_attempt` add column `k8s_policy_revision` int not null default 0;
alter table `task__runner_attempt` add column `k8s_policy_hash` varchar(64) not null default '';
alter table `task__runner_attempt` add column `k8s_denial_rule_id` varchar(128) not null default '';
