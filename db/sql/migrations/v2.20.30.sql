alter table `role` add column `global_permissions` bigint not null default 0;

create table `global_role_state` (
  `id` int primary key,
  `revision` int not null default 1
);
insert into `global_role_state` (`id`, `revision`) values (1, 1);

create table `user__global_role` (
  `id` integer primary key autoincrement,
  `user_id` int not null,
  `role_id` varchar(64) not null,
  `revision` int not null default 1,
  foreign key (`user_id`) references `user` (`id`) on delete cascade,
  foreign key (`role_id`) references `role` (`role_id`) on delete cascade,
  unique (`user_id`, `role_id`)
);
create index `user__global_role__user` on `user__global_role` (`user_id`);
create index `user__global_role__role` on `user__global_role` (`role_id`);

alter table `project__template_role` add column `role_id` varchar(64) null;
alter table `project__template_role` add column `allowed_permissions` bigint not null default 0;
alter table `project__template_role` add column `denied_permissions` bigint not null default 0;
alter table `project__template_role` add column `revision` int not null default 1;

update `project__template_role`
set `role_id`=(
  select `role`.`role_id`
  from `role`
  where `role`.`project_id`=`project__template_role`.`project_id`
    and `role`.`slug`=`project__template_role`.`role_slug`
)
where exists (
  select 1
  from `role`
  where `role`.`project_id`=`project__template_role`.`project_id`
    and `role`.`slug`=`project__template_role`.`role_slug`
);

update `project__template_role`
set `allowed_permissions`=
  case when (`permissions` & 16) <> 0 then 1 else 0 end +
  case when (`permissions` & 1) <> 0 then 2 else 0 end +
  case when (`permissions` & 4) <> 0 then 12 else 0 end;

{{ if .Sqlite }}
create table `project__template_role_v2_20_30` (
  `id`                  integer primary key autoincrement,
  `template_id`         int          not null,
  `role_slug`           varchar(100) not null,
  `project_id`          int          not null,
  `permissions`         bigint       not null default 0,
  `role_id`             varchar(64)  null,
  `allowed_permissions` bigint       not null default 0,
  `denied_permissions`  bigint       not null default 0,
  `revision`            int          not null default 1,

  foreign key (`template_id`) references `project__template` (`id`) on delete cascade,
  foreign key (`project_id`) references `project` (`id`) on delete cascade,
  unique (`template_id`, `role_slug`)
);

insert into `project__template_role_v2_20_30`
  (`id`, `template_id`, `role_slug`, `project_id`, `permissions`, `role_id`,
   `allowed_permissions`, `denied_permissions`, `revision`)
select `id`, `template_id`, `role_slug`, `project_id`, `permissions`, `role_id`,
       `allowed_permissions`, `denied_permissions`, `revision`
from `project__template_role`;

drop table `project__template_role`;
alter table `project__template_role_v2_20_30` rename to `project__template_role`;
{{ else if .Mysql }}
alter table `project__template_role`
  drop foreign key `project__template_role_ibfk_2`;
{{ else }}
alter table `project__template_role`
  drop constraint `project__template_role_role_slug_fkey`;
{{ end }}

create index `project__template_role__role_id`
  on `project__template_role` (`role_id`, `project_id`, `template_id`);
