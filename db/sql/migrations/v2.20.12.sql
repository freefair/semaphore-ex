alter table `user__totp` add column `encrypted_secret` varchar(2048) not null default '';
alter table `user__totp` add column `state` varchar(32) not null default 'active';
alter table `user__totp` add column `confirmed_at` datetime null;
alter table `user__totp` add column `recovery_acknowledged_at` datetime null;
alter table `user__totp` add column `expires_at` datetime null;
alter table `user__totp` add column `last_used_step` bigint null;

create table `user__totp_recovery_code` (
  `id` integer primary key autoincrement,
  `totp_id` integer not null,
  `code_hash` varchar(250) not null,
  `created` datetime not null,
  `consumed_at` datetime null,
  foreign key (`totp_id`) references `user__totp`(`id`) on delete cascade
);

create index `user__totp_recovery_code_unused`
  on `user__totp_recovery_code` (`totp_id`, `consumed_at`, `id`);

create table `user__totp_attempt` (
  `user_id` integer primary key,
  `failure_count` integer not null,
  `window_started` datetime not null,
  `blocked_until` datetime null,
  `updated` datetime not null,
  foreign key (`user_id`) references `user`(`id`) on delete cascade
);

create table `totp_capability_selected_user` (
  `user_id` integer primary key,
  foreign key (`user_id`) references `user`(`id`) on delete cascade
);

create table `totp_capability_transition` (
  `id` integer primary key autoincrement,
  `from_state` varchar(32) not null,
  `to_state` varchar(32) not null,
  `actor_id` integer not null,
  `created` datetime not null
);
