alter table notification_delivery add column provider_record_id varchar(32) not null default '';
alter table notification_delivery add column provider_record_url varchar(1024) not null default '';

create table notification_incident_binding (
 `id` integer primary key{{ if .Mysql }} auto_increment{{ else }} autoincrement{{ end }},
 `destination_id` int not null, `incident_key` varchar(64) not null,
 `provider` varchar(64) not null, `provider_origin` varchar(512) not null,
 `configuration_revision` int not null, `correlation_id` varchar(64) not null,
 `state` varchar(32) not null, `provider_record_id` varchar(32) not null default '',
 `provider_record_url` varchar(1024) not null default '', `owner_delivery_id` int not null,
 `lease_token` varchar(32) not null default '', `lease_until` datetime null,
 `last_reason` varchar(32) not null default '', `created` datetime not null, `updated` datetime not null,
 unique (`destination_id`, `incident_key`), unique (`destination_id`, `correlation_id`)
);
create index notification_incident_binding__lease on notification_incident_binding (`state`, `lease_until`, `id`);
