alter table `project__secret_sync` add `direction` varchar(16) not null default 'read_only';
alter table `project__secret_sync` add `revision` integer not null default 1;

alter table `project__secret_sync_path` add `access_key_id` integer null;
alter table `project__secret_sync_path` add `mount` varchar(128) not null default '';
alter table `project__secret_sync_path` add `field` varchar(128) not null default '';
alter table `project__secret_sync_path` add `remote_version` integer not null default 0;
alter table `project__secret_sync_path` add `content_fingerprint` varchar(80) not null default '';

create table `project__secret_sync_operation` (
  `id` integer primary key autoincrement,
  `request_id` varchar(64) not null,
  `sync_id` integer not null,
  `project_id` integer not null,
  `storage_id` integer not null,
  `sync_revision` integer not null,
  `resolve_operation_id` integer null,
  `requested_by` integer null,
  `status` varchar(16) not null,
  `attempt` integer not null default 0,
  `lease_until` datetime null,
  `created_at` datetime not null,
  `started_at` datetime null,
  `finished_at` datetime null,
  `updated_at` datetime not null,
  `changed_count` integer not null default 0,
  `skipped_count` integer not null default 0,
  `conflict_count` integer not null default 0,
  `error_category` varchar(64) not null default '',
  `outcome` text not null,

  unique (`sync_id`, `request_id`),
  foreign key (`sync_id`) references `project__secret_sync`(`id`) on delete cascade,
  foreign key (`project_id`) references `project`(`id`) on delete cascade,
  foreign key (`storage_id`) references `project__secret_storage`(`id`) on delete cascade,
  foreign key (`resolve_operation_id`) references `project__secret_sync_operation`(`id`) on delete set null,
  foreign key (`requested_by`) references `user`(`id`) on delete set null
);

create index `project__secret_sync_operation_due`
  on `project__secret_sync_operation` (`status`, `lease_until`, `id`);

create index `project__secret_sync_operation_storage`
  on `project__secret_sync_operation` (`project_id`, `storage_id`, `id`);
