{{ if .Postgresql }}
alter table `global_credential` alter column `enabled` drop default;
alter table `global_credential` alter column `enabled` type integer using (case when `enabled` then 1 else 0 end);
alter table `global_credential` alter column `enabled` set default 1;
{{ end }}
