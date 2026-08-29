create table `cluster__node` (
  `boot_id` varchar(64) primary key,
  `node_id` varchar(255) not null,
  `edition` varchar(32) not null,
  `version` varchar(128) not null,
  `build` varchar(128) not null,
  `protocol_version` int not null,
  `schema_version` varchar(32) not null,
  `capabilities` text not null{{ if not .Mysql }} default '[]'{{ end }},
  `started_at` datetime not null,
  `last_seen_at` datetime not null,
  `draining` boolean not null default false,
  `retired_at` datetime null,
  `created` datetime not null
);

create index `cluster__node__node_seen`
  on `cluster__node`(`node_id`, `last_seen_at`, `boot_id`);
