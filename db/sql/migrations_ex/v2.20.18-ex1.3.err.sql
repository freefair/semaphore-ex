-- Restore the historical coarse resource permissions for rollback. The new
-- action bits are EX-owned, so collapsing them is safe for this rollback.
update role
set permissions = (
    permissions & ~36028797014769664
) | case when (permissions & 2401901475790848) <> 0 then 16 else 0 end
  | case when (permissions & 33626895538978816) <> 0 then 4 else 0 end;
