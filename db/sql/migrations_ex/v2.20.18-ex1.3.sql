-- Preserve every original role mask so rollback restores the exact state.
create table ex_role_permissions_2_20_18_ex1_3 (
    role_id varchar(255) primary key,
    permissions bigint not null
);
insert into ex_role_permissions_2_20_18_ex1_3 (role_id, permissions)
select role_id, permissions from role;

-- Split the legacy resource view/manage bits into explicit per-resource
-- actions. read_project admitted the resource lists except repositories,
-- which already required the coarse view bit.
-- The mapping applies equally to project and global roles.
update role
set permissions = (
    permissions
    | case when (permissions & 1048576) <> 0 then 2401900334940160 else 0 end
    | case when (permissions & 16) <> 0 then 2401901475790848 else 0 end
    | case when (permissions & 4) <> 0 then 33626895538978816 else 0 end
) & ~20;
