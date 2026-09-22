create table `ldap_provider` (
  `id` varchar(64) primary key,
  `display_name` varchar(255) not null,
  `state` varchar(32) not null,
  `server_url` varchar(512) not null,
  `tls_mode` varchar(32) not null,
  `trust_mode` varchar(32) not null,
  `ca_pem` text not null,
  `bind_dn` varchar(1024) not null,
  `encrypted_bind_password` text not null,
  `config_version` integer not null default 1,
  `search_base_dn` varchar(1024) not null,
  `user_filter` varchar(2048) not null,
  `identity_attribute` varchar(64) not null,
  `username_attribute` varchar(64) not null,
  `name_attribute` varchar(64) not null,
  `email_attribute` varchar(64) not null,
  `readiness_status` varchar(32) not null,
  `readiness_code` varchar(64) not null,
  `readiness_checked_at` datetime null,
  `recovery_admin_user_id` integer null,
  `recovery_checked_at` datetime null,
  `created` datetime not null,
  `updated` datetime not null,
  foreign key (`recovery_admin_user_id`) references `user`(`id`) on delete set null
);

create table `ldap_provider_selected_user` (
  `provider_id` varchar(64) not null,
  `user_id` integer not null,
  `created` datetime not null,
  primary key (`provider_id`, `user_id`),
  foreign key (`provider_id`) references `ldap_provider`(`id`) on delete cascade,
  foreign key (`user_id`) references `user`(`id`) on delete cascade
);

create table `ldap_auth_attempt` (
  `provider_id` varchar(64) not null,
  `subject_hash` char(64) not null,
  `failure_count` integer not null,
  `window_started` datetime not null,
  `blocked_until` datetime null,
  `updated` datetime not null,
  primary key (`provider_id`, `subject_hash`),
  foreign key (`provider_id`) references `ldap_provider`(`id`) on delete cascade
);

create table `ldap_capability_transition` (
  `id` integer primary key autoincrement,
  `provider_id` varchar(64) not null,
  `from_state` varchar(32) not null,
  `to_state` varchar(32) not null,
  `actor_id` integer not null,
  `created` datetime not null,
  foreign key (`provider_id`) references `ldap_provider`(`id`) on delete cascade
);

create index `ldap_capability_transition_provider`
  on `ldap_capability_transition` (`provider_id`, `id`);
