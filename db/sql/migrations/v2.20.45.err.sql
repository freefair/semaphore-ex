alter table `task__runner_attempt` drop column `k8s_denial_rule_id`;
alter table `task__runner_attempt` drop column `k8s_policy_hash`;
alter table `task__runner_attempt` drop column `k8s_policy_revision`;
alter table `runner` drop column `k8s_policy_hash`;
alter table `runner` drop column `k8s_policy_revision`;
alter table `runner` drop column `k8s_cluster_alias`;
drop table `kubernetes_execution_policy`;
