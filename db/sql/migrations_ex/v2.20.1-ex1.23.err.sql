drop table `cluster__schedule_occurrence`;
{{ if .Mysql }}drop index `task__schedule_occurrence_key` on `task`{{ else }}drop index `task__schedule_occurrence_key`{{ end }};
alter table `task` drop column `schedule_occurrence_key`;
