-- Restore the exact masks present when the forward migration ran. The
-- read_project compatibility grants cannot be inferred from fine bits alone.
update role
set permissions = (
    select permissions
    from ex_role_permissions_2_20_18_ex1_3
    where ex_role_permissions_2_20_18_ex1_3.role_id = role.role_id
)
where role_id in (select role_id from ex_role_permissions_2_20_18_ex1_3);
drop table ex_role_permissions_2_20_18_ex1_3;
