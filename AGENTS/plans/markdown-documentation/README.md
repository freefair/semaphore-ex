# Plain Markdown documentation

## Scope

Convert the documentation submodule to directly readable Markdown, remove the
documentation website and its build/deployment dependencies, and document every
configuration parameter in the checked-in reference.

## Approach

1. Preserve the existing docs submodule and page paths to minimize product-link
   churn. Convert MDX components, tabs, admonitions, images and links to Markdown.
   Preserve translated prose and provide explicit links to canonical English
   pages wherever the site previously supplied an implicit fallback.
2. Replace site navigation with README indexes and the dynamic capability table
   with a static Markdown table. Remove site-only source and build/deploy jobs.
3. Extend `tools/docsref` to describe nested map/array members, document external
   logger and encryption-file schemas, and emit ordinary Markdown. Keep generated
   references committed; readers never run a generator or compiler.
4. Replace the site checker with dependency-free Markdown/link validation; update
   the root verification integration, README and working agreements.
5. Verify complete parameter coverage, generation reproducibility, local links,
   absence of upstream website references and absence of MDX/build requirements.
   Commit docs and product integration separately without pushing.

## Alternatives

- Keeping Docusaurus alongside Markdown preserves the unwanted UI/toolchain and
  was explicitly rejected by the user.
- Importing docs into the product repository changes repository ownership and
  upstream maintenance unnecessarily; preserve the existing submodule.
- Hand-maintaining all parameter names drifts from the parser; keep an optional
  maintainer generator while publishing its full Markdown output in Git.

## Verification

Use source-backed reference generation, focused generator checks, dependency-free
link/anchor checks, and diff inspection. No product UI or application behavior is
changed, and no documentation site is built or deployed.
