-- Split the legacy resource view/manage bits into explicit per-resource
-- actions. The mapping is lossless because these bits did not exist before
-- this EX migration. It applies equally to project and global template roles.
update role
set permissions = (
    permissions
    | case when (permissions & 16) <> 0 then 2401901475790848 else 0 end
    | case when (permissions & 4) <> 0 then 33626895538978816 else 0 end
) & ~20;
