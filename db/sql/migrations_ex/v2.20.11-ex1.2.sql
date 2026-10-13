{{if .Mysql}}
alter table `cluster__node` modify column `schema_version` text not null;
{{else if .Postgresql}}
alter table `cluster__node` alter column `schema_version` type text;
{{end}}
