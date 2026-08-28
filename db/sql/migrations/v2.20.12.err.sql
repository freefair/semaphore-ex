drop table `totp_capability_transition`;
drop table `totp_capability_selected_user`;
drop table `user__totp_attempt`;
drop table `user__totp_recovery_code`;

alter table `user__totp` drop column `last_used_step`;
alter table `user__totp` drop column `expires_at`;
alter table `user__totp` drop column `recovery_acknowledged_at`;
alter table `user__totp` drop column `confirmed_at`;
alter table `user__totp` drop column `state`;
alter table `user__totp` drop column `encrypted_secret`;
