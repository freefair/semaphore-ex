{{ if .Mysql }}drop index `project__template_role__role_id` on `project__template_role`{{ else }}drop index `project__template_role__role_id`{{ end }};
{{ if .Sqlite }}
create table `project__template_role_v2_20_30_rollback` (
  `id`          integer primary key autoincrement,
  `template_id` int          not null,
  `role_slug`   varchar(100) not null,
  `project_id`  int          not null,
  `permissions` bigint       not null default 0,

  foreign key (`template_id`) references `project__template` (`id`) on delete cascade,
  foreign key (`role_slug`) references `role` (`slug`) on delete cascade,
  foreign key (`project_id`) references `project` (`id`) on delete cascade,
  unique (`template_id`, `role_slug`)
);

insert into `project__template_role_v2_20_30_rollback`
  (`id`, `template_id`, `role_slug`, `project_id`, `permissions`)
select `id`, `template_id`, `role_slug`, `project_id`, `permissions`
from `project__template_role`;

drop table `project__template_role`;
alter table `project__template_role_v2_20_30_rollback` rename to `project__template_role`;
{{ else }}
{{ if .Mysql }}
alter table `project__template_role`
  add constraint `project__template_role_ibfk_2`
  foreign key (`role_slug`) references `role` (`slug`) on delete cascade;
{{ else }}
alter table `project__template_role`
  add constraint `project__template_role_role_slug_fkey`
  foreign key (`role_slug`) references `role` (`slug`) on delete cascade;
{{ end }}
alter table `project__template_role` drop column `revision`;
alter table `project__template_role` drop column `denied_permissions`;
alter table `project__template_role` drop column `allowed_permissions`;
alter table `project__template_role` drop column `role_id`;
{{ end }}

drop table `user__global_role`;
drop table `global_role_state`;

alter table `role` drop column `global_permissions`;
