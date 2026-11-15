create table `global_secret_storage` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `name` varchar(128) not null, `type` varchar(32) not null, `params` text not null,
 `credential_key_id` int null, `revision` int not null,
 `created` datetime not null, `updated` datetime not null,
 unique (`name`),
 foreign key (`credential_key_id`) references `access_key`(`id`) on delete set null
);
create table `global_secret_storage_project` (
 `storage_id` int not null, `project_id` int not null,
 primary key (`storage_id`, `project_id`),
 foreign key (`storage_id`) references `global_secret_storage`(`id`) on delete cascade,
 foreign key (`project_id`) references `project`(`id`) on delete cascade
);
create index `global_secret_storage_project__project`
 on `global_secret_storage_project` (`project_id`);
