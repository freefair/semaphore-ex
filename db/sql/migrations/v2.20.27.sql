alter table `cluster__task_control` add column `last_evidence_status` varchar(32) null;
alter table `cluster__task_control` add column `previous_owner_boot_id` varchar(64) null;
alter table `cluster__task_control` add column `ownership_transferred_at` datetime null;
alter table `cluster__task_control` add column `assignment_revoked_at` datetime null;
alter table `cluster__task_control` add column `last_recovery_decision` varchar(16) null;
alter table `cluster__task_control` add column `last_recovery_reason` varchar(512) null;
alter table `cluster__task_control` add column `last_recovery_safe_replacement` int not null default 0;
alter table `cluster__task_control` add column `last_recovery_at` datetime null;
alter table `task` add column `task_control_fencing_token` bigint not null default 0;
