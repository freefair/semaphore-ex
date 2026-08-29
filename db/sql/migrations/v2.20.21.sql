alter table `project__workflow_run` add column `desired_state` varchar(20) not null default 'running';

create index `project__workflow_run__desired_state`
  on `project__workflow_run`(`desired_state`, `status`, `id`);
