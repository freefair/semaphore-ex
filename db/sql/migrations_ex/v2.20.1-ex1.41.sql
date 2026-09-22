alter table `docker_reconciliation_orphan_candidate` add column `identity` varchar(128) not null default '';
alter table `docker_reconciliation_remediation_command` add column `candidate_session_id` varchar(128) not null default '';
alter table `docker_reconciliation_remediation_command` add column `candidate_identity` varchar(128) not null default '';
