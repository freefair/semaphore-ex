# Upstream permissions compatibility sync

## Scope

Merge upstream `87bcc0cbde07ad002f6c0cf7b5e369812e8af4fa` into product
`develop` at `7a006a0c83aa6b98431ce27a5e081e4c4c12e811`, preserving
Infisical PAM, immutable EX role identities, independent migrations, explicit
denies, workflow governance, and the full-product build.

## Approach

1. Retain the fetched assessment and recovery branch; preserve unrelated files.
2. Integrate workflow grants and project/object visibility into the existing EX
   authorization authority. Preserve all published permission bits; assign new
   read-project and approve-workflow bits instead of importing colliding masks.
3. Adapt new canonical migrations 2.20.17 and 2.20.18 to EX string role IDs and
   permission representation. Preserve every shipped migration checksum.
4. Extend backup/restore with explicit permission-schema provenance and
   source-format-aware conversion, retaining role regeneration and deny masks.
5. Integrate upstream UI visibility, workflow grant editing, audit presentation,
   and hidden-resource handling while keeping existing EX controls.
6. Review new exported contracts and update inventories only after behavior tests.
7. Document decisions and behavior in the product Wiki. Obtain independent Claude
   and dedicated Terra review, run all retained gates with Go 1.26.10 and Node
   24.19.0, and verify desktop/mobile workflows and permission boundaries.
8. Re-fetch origin, publish product and Wiki normally, verify exact remote SHAs
   and required CI, then remove task-owned fixtures and temporary tooling.

## Alternatives and rationale

Importing upstream numeric permission masks or replacing EX role storage would
reinterpret existing grants and lose governance guarantees. Keeping only the EX
side would omit upstream functionality. Semantic adaptation at the existing
boundaries preserves both feature sets without rewriting published history.

No release tag, deployment, shared infrastructure mutation, helper refactor, or
Terraform provider change belongs to this sync.
