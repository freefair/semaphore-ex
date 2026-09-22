alter table `notification_destination` add column `configuration_revision` int not null default 1;
alter table `notification_delivery` add column `destination_configuration_revision` int not null default 0;
update `notification_destination` set `configuration_revision`=1;
update `notification_delivery` set `destination_configuration_revision`=0;
