{{ if .Mysql }}
alter table notification_destination add column provider_config longtext null;
update notification_destination set provider_config='' where provider_config is null;
alter table notification_destination modify column provider_config longtext not null;
{{ else }}
alter table notification_destination add column provider_config longtext not null default '';
{{ end }}
alter table notification_delivery add column provider_request_id varchar(128) not null default '';
