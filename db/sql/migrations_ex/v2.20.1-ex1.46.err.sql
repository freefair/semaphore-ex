drop table `kubernetes_reconciliation_command`;
drop table `kubernetes_reconciliation_candidate`;
drop table `kubernetes_reconciliation_observation`;
drop table `kubernetes_reconciliation_target`;
drop table `kubernetes_reconciliation_session`;
alter table `runner` drop column `k8s_namespace`;
