create table `project__environment_credential` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `project_id` int not null, `environment_id` int not null, `credential_id` int not null,
 `name` varchar(128) not null, `target` varchar(8) not null,
 unique (`environment_id`, `name`),
 foreign key (`project_id`) references `project`(`id`) on delete cascade,
 foreign key (`environment_id`) references `project__environment`(`id`) on delete cascade,
 foreign key (`credential_id`) references `global_credential`(`id`) on delete cascade
);
create index `project__environment_credential__credential`
 on `project__environment_credential` (`credential_id`);
