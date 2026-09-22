alter table `project__workflow_node` add column `approval_permission` bigint not null default 1;
alter table `project__workflow_node` add column `approval_timeout_outcome` varchar(20) not null default 'reject';
alter table `project__workflow_node` add column `approval_separation_of_duties` int not null default 0;

alter table `project__workflow_approval` add column `deadline` datetime null;
alter table `project__workflow_approval` add column `prompt` text not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_approval` add column `eligible_permission` bigint not null default 1;
alter table `project__workflow_approval` add column `separation_of_duties` int not null default 0;
alter table `project__workflow_approval` add column `request_actor_user_id` int not null default 0;
alter table `project__workflow_approval` add column `timeout_outcome` varchar(20) not null default 'reject';
alter table `project__workflow_approval` add column `decision_comment` text not null{{ if not .Mysql }} default ''{{ end }};
alter table `project__workflow_approval` add column `decision_source` varchar(20) not null default '';
alter table `project__workflow_approval` add column `correlation_id` varchar(128) not null default '';

create index `project__workflow_approval__pending_deadline`
  on `project__workflow_approval`(`status`, `deadline`, `id`);
