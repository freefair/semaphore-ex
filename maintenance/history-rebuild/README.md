# Fork History Ownership Review

## Review the Candidate

This is a separately authorized local reconstruction, followed by regular upstream merges for future updates.
Publication requires separate approval.
The original root and docs histories remain recoverable.

```bash
git diff develop..codex/fork-history-rebuilt
git log --oneline codex/fork-history-rebuilt --not upstream/develop
```

[Root commit decisions](root-commits.yml) cover all 246 fork-only commits from `eb716d4733b40a3a1d18084e7d26ff7ddf8a2425` above upstream `9cd15a7604eb3486134eaa32944d5dcf16a24655`.
Each entry records its original and reconstructed identity, original patch checksum, changed paths and ownership decision.
Two journal-only commits map to their retained parent, leaving 244 reconstructed commits before the final maintenance documentation commit.
Original authors, timestamps, commit messages and mapped ancestry are preserved for reconstructed commits.
Old commit signatures cannot cover the new objects; the reconstructed historical objects are unsigned.

[Documentation decisions](docs-commits.yml) cover all 115 fork-only documentation commits through `516b00df81c4d41161990f2a36ffdfdf81a17331`, above upstream `b1f309193c66800463c8e68f25c729e091535323`.
Their history stays intact: dedicated feature pages are already isolated, and corrections to existing pages, navigation and translated fallbacks remain necessary to describe the selected product.
A separate documentation commit updates the current maintenance runbook for the new source boundaries.

## Ownership Result

[Shared-file decisions](shared-files.yml) connect the commit assessment to 219 original shared paths and their introducing/modifying commits.
[Source boundaries](../source-boundaries.md) explains where future changes belong.
The [follow-up separation review](../source-separation/README.md) records additional source boundaries and updated overlap measurements.

- 907 new Go declaration names move into 87 focused same-package files, preserving public type identity and existing integration calls.
- Task-summary persistence and its factory belong to the selected Enhanced module; the selected UI uses `EnhancedTaskSummary.vue`.
- Independent computed/method additions from 30 Vue hosts live in explicit option modules. Members that close over module-local declarations stay with those declarations.
- 619 added English/German messages live under `web/src/lang/enhanced`; merged locale values remain unchanged.
- Fork-only API definitions and paths live in `api-docs-ex.yml`; the approved bundler supplies the unchanged expanded contract to Dredd.
- Governance routes and fork-only Task targets have focused files, preserving middleware order and public task names.

[Metrics](metrics.yml) compare textual changes in upstream-owned files using `git diff --no-renames --numstat` against the same upstream commit.
Changed shared lines fall from 38,502 to 15,983, about 58.5%.
The number of touched shared text paths changes from 219 to 220 because explicit integration entries remain necessary.
Line reduction is a measure of textual overlap, not a guarantee against future semantic conflicts.

## Preservation Evidence

The assessment combines per-commit ownership classification, shared-delta review, structural snapshot comparisons, focused security review and behavior gates.
It is not a new independent semantic audit of every original feature.

Every reconstructed snapshot preserves SQL migration bytes, the numeric registry and the migration ledger.
The Go comparison verifies unchanged declaration tokens across same-package moves; 89 distinct transformed snapshots cover all 246 original commits.
The Vue comparison expands imported options and checks component option ASTs, module-local declarations, templates and styles; 102 distinct component snapshots cover all 246 commits.
Summary ownership, governance-route extraction, the new bundler and bootstrap-reference remapping have separate targeted review and tests.
The API and locale transformers assert equal expanded values before recording each changed snapshot.

The original and reconstructed checkpoint 20 both fail compilation with the same pre-existing missing `path` import in `services/tasks/local_executor.go`.
Both checkpoint 244 variants compile.
The reconstruction preserves the timing of original compatibility repairs; it does not claim that every historical commit builds independently.

The verified implementation passes root/Enhanced tests and vet, maintenance checks, API bundler tests, Dredd hook compilation, product build, Dockerfile checks, eleven-locale documentation builds and canonical fallback checks.
MySQL 8.4, MariaDB 10.11 and PostgreSQL 12.22 run the actual `TestMigrationMatrix`; SQLite is included in the root suite.
The complete frontend suite retains 249 passing tests and the same three baseline failures in ArgsPicker, YesNoDialog and Socket.
Dredd processes the original and bundled specifications with the same 438 dry-run transactions.

Desktop/mobile browser checks exercise the compiled UI with synthetic API fixtures for task summaries, partial results, runner display, and workflow delay validation.
Persistence and permission behavior are verified separately by the Go suites.
The mobile workflow editor's horizontal overflow also reproduces on the exact original root commit.
It is a separate existing UI issue, not an effect of file separation.
The dedicated Terra reviews found no security-preservation regression, including permission/admission helpers and symlink-safe API output handling.

Retain the full local gate logs, fingerprints, browser artifacts, structural reports and final HA report with the publication review.
Remote CI and publication checks belong to the separately authorized publication step.
