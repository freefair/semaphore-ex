alter table `runner` add `version` varchar(128) not null default '';
alter table `runner` add `platform` varchar(128) not null default '';
alter table `runner` add `current_load` integer not null default 0;
alter table `task` add `runner_id_snapshot` integer null;
update `task` set `runner_id_snapshot`=`runner_id` where `runner_id` is not null;
