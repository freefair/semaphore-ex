{{ if .Mysql }}drop index `project__template__project_id_name` on `project__template`{{ else }}drop index `project__template__project_id_name`{{ end }};
