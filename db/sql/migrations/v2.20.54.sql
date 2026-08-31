alter table notification_destination add column provider_config longtext not null default '';
alter table notification_delivery add column provider_request_id varchar(128) not null default '';
