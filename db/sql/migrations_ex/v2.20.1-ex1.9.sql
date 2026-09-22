create table `audit_webhook_config` (
    `id` integer not null primary key,
    `endpoint` varchar(2048) not null,
    `encrypted_credential` text not null,
    `credential_configured` integer not null default 0,
    `paused` integer not null default 0,
    `created` datetime not null,
    `updated` datetime not null
);

create table `audit_webhook_delivery` (
    `id` integer primary key autoincrement,
    `event_id` varchar(32) not null,
    `payload` text not null,
    `status` varchar(16) not null,
    `attempts` integer not null default 0,
    `next_attempt` datetime not null,
    `lease_until` datetime null,
    `http_status` integer null,
    `last_error` varchar(512) not null default '',
    `created` datetime not null,
    `updated` datetime not null,
    `delivered_at` datetime null,
    unique (`event_id`)
);

create index `audit_webhook_delivery_due`
    on `audit_webhook_delivery` (`status`, `next_attempt`, `id`);
