{{ if .Postgresql }}
alter table `global_credential` alter column `enabled` drop default;
alter table `global_credential` alter column `enabled` type boolean using (`enabled`::text::boolean);
alter table `global_credential` alter column `enabled` set default true;
{{ end }}
