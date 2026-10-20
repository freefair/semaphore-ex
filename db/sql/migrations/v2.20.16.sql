-- EX already owns global-role identity, assignments, revision/CAS and
-- last-admin serialization. Keep the canonical upstream migration identity as
-- an explicit bridge, but do not create a second integer role engine or
-- collide with EX's user__global_role authority.
select 1;
