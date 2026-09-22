create table `cluster__workflow_reconciliation` (
  `project_id` int not null,
  `workflow_run_id` int not null,
  `owner_boot_id` varchar(64) not null,
  `previous_owner_boot_id` varchar(64) null,
  `fencing_token` bigint not null,
  `lease_expires_at` datetime not null,
  `acquired_at` datetime not null,
  `ownership_transferred_at` datetime null,
  `transfer_count` int not null default 0,
  `operation_sequence` bigint not null default 0,
  `last_reconciled_at` datetime null,
  `created` datetime not null,
  `updated` datetime not null,
  primary key (`project_id`, `workflow_run_id`)
);

create index `cluster__workflow_reconciliation__lease`
  on `cluster__workflow_reconciliation`(`lease_expires_at`);

alter table `project__workflow_run_node`
  add column `progression_fencing_token` bigint not null default 0;
