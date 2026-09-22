drop table `docker_reconciliation_remediation_command`;
alter table `docker_reconciliation_orphan_candidate` drop column `remediated_at`;
alter table `docker_reconciliation_orphan_candidate` drop column `status`;
alter table `docker_reconciliation_orphan_candidate` drop column `revision`;
