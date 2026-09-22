drop table `ldap_group_reconciliation`;
{{ if .Mysql }}drop index `project__user__ldap_group_managed_assignment` on `project__user`{{ else }}drop index `project__user__ldap_group_managed_assignment`{{ end }};
alter table `project__user` drop column `ldap_group_managed_assignment_id`;
drop table `ldap_group_managed_assignment`;
drop table `ldap_group_mapping`;
drop table `ldap_group_mapping_state`;

alter table `ldap_provider` drop column `group_max_depth`;
alter table `ldap_provider` drop column `group_member_attribute`;
alter table `ldap_provider` drop column `group_identity_attribute`;
alter table `ldap_provider` drop column `group_filter`;
alter table `ldap_provider` drop column `group_user_filter`;
alter table `ldap_provider` drop column `group_search_base_dn`;
