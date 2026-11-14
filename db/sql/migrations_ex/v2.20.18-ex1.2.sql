-- LDAP diagnostic details and group labels are EX-owned administrative data.
-- Both remain secret-free: readiness details are sanitized by the service and
-- group names are display metadata only.
alter table `ldap_provider` add column `readiness_detail` varchar(1024) not null default '';
alter table `ldap_provider` add column `group_name_attribute` varchar(100) not null default 'cn';
alter table `ldap_group_mapping` add column `group_name` varchar(255) not null default '';
