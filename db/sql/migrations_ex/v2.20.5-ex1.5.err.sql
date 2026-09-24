drop table if exists `inventory__snapshot_host`;
drop table if exists `inventory__snapshot`;
alter table `runner` drop column `inventory_refresh_version`;
