create table `capability_config` (
  `capability_id` varchar(64) primary key,
  `state` varchar(32) not null,
  `expires_at` datetime null,
  `updated` datetime not null
);

create table `capability_test_record` (
  `id` integer primary key autoincrement,
  `value` varchar(256) not null,
  `source` varchar(16) not null,
  `created` datetime not null
);
