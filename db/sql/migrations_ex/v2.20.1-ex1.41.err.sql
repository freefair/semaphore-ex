alter table `docker_reconciliation_remediation_command` drop column `candidate_identity`;
alter table `docker_reconciliation_remediation_command` drop column `candidate_session_id`;
alter table `docker_reconciliation_orphan_candidate` drop column `identity`;
