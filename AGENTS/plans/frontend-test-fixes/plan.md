# Frontend Test Repairs

## Scope

Repair the normal frontend test command on local `develop` starting at `72ae365d`.
The user explicitly authorizes the previously pending test-tooling scope.
Publication and HA recovery implementation remain separate.

## Approach

1. Preserve Vue template ES-module exports in the Mocha/Webpack test build through the Babel preset's supported transform exclusions, scoped to the test environment in `web/babel.config.js`.
   A global internal `VUE_CLI_TEST` flag worked diagnostically but couples the suite to unrelated Vue CLI internals; a scoped Babel configuration is preferable.
2. Repair the obsolete fixtures in `web/tests/unit/args-picker.spec.js` and `example.spec.js`: emit component events through proper stubs, supply translation context, and assert the actual form/dialog contract rather than incidental whitespace or a nonexistent prop.
3. Review and repair the obsolete eager-socket test in `web/tests/unit/lib/Socket.spec.js` against the current lazy/session-aware API. Keep unrelated implementation findings separate.
4. Run the unmodified normal test command without diagnostic flags, lint changed files, and build production assets to verify the non-test Babel configuration. Keep failed output and perform a final diff review.
5. Document the supported test configuration and evidence, then commit locally. Do not push.

## Verification

The previous retained normal run is 250 passing / 6 failing. The diagnostic environment produces 253 passing / 3 failing, matching the three failures of the exact starting source.
The new rendered-component tests remain enabled and must pass through the default command.
Security-sensitive session tests receive the repository-required dedicated Terra review.

## Result

The normal frontend suite passes all 263 tests with neither `VUE_CLI_TEST` nor `NODE_OPTIONS`.
Changed-file ESLint, production build and the retained desktop/mobile browser checks pass.
The Babel exclusions apply only in the test environment; no application source changes are included.
See `maintenance/frontend-tests.md` for the supported setup and the separately reported Socket transition defect.
