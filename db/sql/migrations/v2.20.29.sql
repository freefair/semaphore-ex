alter table `role` add column `role_id` varchar(64) null;
alter table `role` add column `revision` int not null default 1;
update `role` set `role_id`=`slug` where `role_id` is null;
update `role` set `permissions`=`permissions` | 16 where `project_id` is not null;
create unique index `role__role_id` on `role`(`role_id`);
create unique index `role__project_name` on `role`(`name`, `project_id`);

alter table `project__user` add column `role_id` varchar(64) null;
alter table `project__user` add column `revision` int not null default 1;
update `project__user`
set `role_id`=(
  select `role`.`role_id`
  from `role`
  where `role`.`project_id`=`project__user`.`project_id`
    and `role`.`slug`=`project__user`.`role`
)
where exists (
  select 1
  from `role`
  where `role`.`project_id`=`project__user`.`project_id`
    and `role`.`slug`=`project__user`.`role`
);
create index `project__user__role_id` on `project__user`(`role_id`);
