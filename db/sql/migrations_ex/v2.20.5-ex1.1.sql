create table project__terraform_inventory_lock (
    project_id integer not null,
    inventory_id integer not null,
    lock_id varchar(255) not null,
    lock_info text not null,
    created datetime not null,
    primary key (project_id, inventory_id),
    foreign key (project_id) references project(id) on delete cascade,
    foreign key (inventory_id) references project__inventory(id) on delete cascade
);

create table project__terraform_inventory_state_tombstone (
    id integer primary key autoincrement,
    project_id integer not null,
    inventory_id integer not null,
    state_id integer not null,
    created datetime not null,
    foreign key (project_id) references project(id) on delete cascade,
    foreign key (inventory_id) references project__inventory(id) on delete cascade
);

create index project__terraform_inventory_state_tombstone_scope
    on project__terraform_inventory_state_tombstone(project_id, inventory_id, created);
