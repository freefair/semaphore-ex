create table `project__workflow_version` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `project_id` int not null, `workflow_template_id` int not null, `version_number` int not null,
 `parent_version_id` int null, `restored_from_version_id` int null, `author_user_id` int not null,
 `message` varchar(512) not null default '', `created` datetime not null,
 `content_fingerprint` varchar(71) not null, `definition_snapshot` longtext not null,
 unique (`project_id`, `workflow_template_id`, `version_number`),
 foreign key (`workflow_template_id`) references `project__workflow_template`(`id`) on delete cascade
);
create index `project__workflow_version__timeline`
 on `project__workflow_version` (`project_id`, `workflow_template_id`, `version_number`);
create table `project__template_version` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `owner_project_id` int not null, `template_id` int not null, `version_number` int not null,
 `author_user_id` int not null, `created` datetime not null,
 `content_fingerprint` varchar(71) not null, `execution_snapshot` longtext not null,
 unique (`owner_project_id`, `template_id`, `version_number`)
);
create index `project__template_version__timeline`
 on `project__template_version` (`owner_project_id`, `template_id`, `version_number`);
create table `project__cross_project_template_grant` (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `owner_project_id` int not null, `consumer_project_id` int not null, `template_id` int not null,
 `min_template_version` int not null, `max_template_version` int not null,
 `operations` int not null, `status` varchar(16) not null, `revision` int not null,
 `reason` varchar(512) not null, `created_by_user_id` int not null, `created` datetime not null,
 `accepted_by_user_id` int null, `accepted_at` datetime null,
 `revoked_by_user_id` int null, `revoked_at` datetime null, `revocation_reason` varchar(512) not null default ''
);
create index `project__cross_project_template_grant__owner`
 on `project__cross_project_template_grant` (`owner_project_id`, `template_id`, `status`, `id`);
create index `project__cross_project_template_grant__consumer`
 on `project__cross_project_template_grant` (`consumer_project_id`, `status`, `id`);
create table `project__cross_project_template_grant_version` (
 `grant_id` int not null, `template_version_id` int not null,
 primary key (`grant_id`, `template_version_id`),
 foreign key (`grant_id`) references `project__cross_project_template_grant`(`id`) on delete cascade,
 foreign key (`template_version_id`) references `project__template_version`(`id`) on delete restrict
);
create index `project__cross_project_template_grant_version__template_version`
 on `project__cross_project_template_grant_version` (`template_version_id`);
alter table `project__workflow_run` add column `workflow_version_id` int not null default 0;
{{ if .Mysql }}
alter table `project__workflow_node` add column `cross_project_template_reference` longtext null;
update `project__workflow_node` set `cross_project_template_reference` = '' where `cross_project_template_reference` is null;
alter table `project__workflow_node` modify column `cross_project_template_reference` longtext not null;
alter table `project__workflow_run_node` add column `cross_project_template_provenance` longtext null;
update `project__workflow_run_node` set `cross_project_template_provenance` = '' where `cross_project_template_provenance` is null;
alter table `project__workflow_run_node` modify column `cross_project_template_provenance` longtext not null;
{{ else }}
alter table `project__workflow_node` add column `cross_project_template_reference` longtext not null default '';
alter table `project__workflow_run_node` add column `cross_project_template_provenance` longtext not null default '';
{{ end }}
alter table `task` add column `workflow_template_provenance` longtext null;
