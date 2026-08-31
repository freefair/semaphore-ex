alter table `notification_destination` add column `region` varchar(8) not null default '';
alter table `notification_delivery` add column `destination_region` varchar(8) not null default '';
update `notification_destination` set `region`='us' where `provider`='pagerduty';
update `notification_delivery` set `destination_region`='us' where `destination_provider`='pagerduty';
