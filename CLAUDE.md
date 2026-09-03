# Project Working Agreement

## Product Model

This branch ships one full-featured product edition containing every feature implemented by this
branch. It does not ship or support a separate Community edition, edition selection, commercial
subscriptions, license activation, or subscription quotas. The product build always selects the
clean-room implementation; Community stubs may remain only as unwired compatibility scaffolding
where removing them would unnecessarily obstruct upstream merges.

Every implemented feature is included without entitlement checks, but inclusion does not force the
feature to be enabled. Features that are optional, noisy, or require external infrastructure may
have ordinary configuration flags so an operator can disable their behavior and hide their UI.
Edition and subscription gates must not control those flags. Backend authorization, role
permissions, safety policy, configured enablement, and operational lifecycle states remain enforced
independently; they are security and runtime controls rather than product-edition gates.

## Autonomy

During execution of the approved enhanced-edition slice plan, resolve local, reversible implementation and design details with best engineering judgment. Continue without asking for routine preferences. Ask Dennis only when work is blocked by missing authority or information, an irreversible or destructive action, or a choice that materially changes the approved product scope.

The local QA server, database, and test data were created by Codex for this plan. They are Codex-owned disposable infrastructure and may be migrated, rebuilt, reset, seeded, restarted, or stopped without asking Dennis. This authorization does not extend to remote or shared environments.

Keep enhanced-edition work upstream-compatible. Prefer implementing existing interfaces and extension seams; leave Community behavior and shared UI untouched wherever possible. UI changes must be the smallest integration needed for the selected slice and should reuse existing routes, views, and components instead of redesigning shared surfaces.

Keep docs/docs/developer-guide/plans/pro-slices/STATUS.md up to date.

## Security Execution

Run security scans, security-focused investigation, security-relevant implementation, security reviews, and security-fix verification through a dedicated `gpt-5.6-terra` sub-agent when delegation is available. The primary agent integrates the resulting evidence and runs non-security release gates.
