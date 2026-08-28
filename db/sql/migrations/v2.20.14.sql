alter table `project__workflow_template` add column `definition_version` int not null default 1;
alter table `project__workflow_template` add column `revision` int not null default 1;
alter table `project__workflow_node` add column `display_name` varchar(255) not null default '';
alter table `project__workflow_edge` add column `label` varchar(255) not null default '';
