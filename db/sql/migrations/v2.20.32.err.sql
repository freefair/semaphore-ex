drop table `oidc_group_reconciliation`;
{{ if .Mysql }}drop index `project__user__oidc_group_managed_assignment` on `project__user`{{ else }}drop index `project__user__oidc_group_managed_assignment`{{ end }};
alter table `project__user` drop column `oidc_group_managed_assignment_id`;
drop table `oidc_group_managed_assignment`;
drop table `oidc_group_mapping`;
drop table `oidc_group_mapping_state`;
