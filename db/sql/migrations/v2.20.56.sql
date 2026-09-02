create table `global_credential` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `type` varchar(32) not null, `display_name` varchar(128) not null,
 `owner_user_id` int not null, `enabled` integer not null default 1,
 `revision` int not null, `current_version` int not null,
 `created` datetime not null, `updated` datetime not null
);
create table `global_credential_version` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `credential_id` int not null, `version` int not null, `fingerprint` varchar(64) not null,
 `material_kind` varchar(32) not null, `encrypted_material` longtext not null,
	`external_reference` varchar(2048) not null default '', `created_by_user_id` int not null,
 `created` datetime not null,
 unique (`credential_id`, `version`), unique (`credential_id`, `fingerprint`),
 foreign key (`credential_id`) references `global_credential`(`id`) on delete cascade
);
create table `global_credential_grant` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `credential_id` int not null, `project_id` int not null, `operations` bigint not null,
 `expires_at` datetime null, `status` varchar(16) not null, `revision` int not null,
 `created_by_user_id` int not null, `revoked_by_user_id` int null, `revoked_at` datetime null,
 `created` datetime not null, `updated` datetime not null,
 unique (`credential_id`, `project_id`),
 foreign key (`credential_id`) references `global_credential`(`id`) on delete restrict,
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `global_credential_grant__project_effective`
 on `global_credential_grant` (`project_id`, `status`, `expires_at`, `id`);
