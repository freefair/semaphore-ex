alter table `runner` add `registration_policy` varchar(16) not null default 'standard';
alter table `runner` add `registration_kind` varchar(16) not null default 'shared';
alter table `runner` add `security_compliant` boolean not null default true;
alter table `runner` add `security_reason` varchar(255) not null default 'standard registration policy accepted';
alter table `runner` add `security_remediation` varchar(512) not null default '';
alter table `runner` add `transport_trust` varchar(32) not null default 'plaintext';
alter table `runner` add `security_protocol_version` integer not null default 0;
alter table `runner` add `security_checked_at` datetime null;
