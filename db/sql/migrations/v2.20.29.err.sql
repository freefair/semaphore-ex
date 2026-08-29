{{ if .Mysql }}drop index `project__user__role_id` on `project__user`{{ else }}drop index `project__user__role_id`{{ end }};
alter table `project__user` drop column `revision`;
alter table `project__user` drop column `role_id`;

{{ if .Mysql }}drop index `role__project_name` on `role`{{ else }}drop index `role__project_name`{{ end }};
{{ if .Mysql }}drop index `role__role_id` on `role`{{ else }}drop index `role__role_id`{{ end }};
update `role` set `permissions`=`permissions` & ~16 where `project_id` is not null;
alter table `role` drop column `revision`;
alter table `role` drop column `role_id`;
