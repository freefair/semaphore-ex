drop table `project__secret_sync_operation`;

alter table `project__secret_sync_path` drop column `content_fingerprint`;
alter table `project__secret_sync_path` drop column `remote_version`;
alter table `project__secret_sync_path` drop column `field`;
alter table `project__secret_sync_path` drop column `mount`;
alter table `project__secret_sync_path` drop column `access_key_id`;

alter table `project__secret_sync` drop column `revision`;
alter table `project__secret_sync` drop column `direction`;
