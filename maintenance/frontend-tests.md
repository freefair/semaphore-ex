# Frontend Unit Test Setup

## Quick Start

Use the repository's Node toolchain and the committed frontend lockfile.
From the repository root:

```bash
cd web
npm ci
npm run test:unit
npm run build
```

The normal test command needs no `VUE_CLI_TEST` or local-storage diagnostic flag.
The configured output directory is also used by the embedded frontend; the production
build restores production assets after the Mocha bundle has been generated.

## Module Compilation

The Vue CLI Babel preset enables CommonJS transformation when `NODE_ENV=test`.
The Mocha runner bundles with Webpack, and Vue/Vuetify template loading introduces
ES-module imports around the generated template exports. Converting those exports
to CommonJS leaves the loader unable to import `render` and `staticRenderFns`.
Components then mount without their compiled render functions.

`web/babel.config.js` excludes `transform-modules-commonjs` and its dependent
`transform-dynamic-import` only when Babel's environment is `test`.
Webpack retains responsibility for module bundling. Production and development use
the original preset behavior. The internal `VUE_CLI_TEST` switch is unnecessary.

## Component and Socket Contracts

Argument-picker stubs forward input and keyboard events through Vue component
emissions, matching the component event boundary used by Vuetify. The tests cover
trimmed input, Enter submission, form submission, rejected validation and unchanged
caller-owned arrays. Each test gets its own Vuetify instance and uses a local Vue
constructor so plugin registration does not change other suites.

Confirmation-dialog tests supply translation context and the actual `title`/`text`
props. They assert visible labels, confirmation/cancellation events, the bound close
notification, custom labels and hiding the cancel action.

Socket tests follow the existing application call order: activate the session, then
start the real transport. They verify listener delivery/removal and transport closure
when the session ends. Global WebSocket fixtures are restored in `finally` blocks.

## Verification Record

At the source-separation checkpoint the normal suite reported 250 passing and six
failures. The separately authorized repair reports 263 passing and zero failures
with the ordinary `npm run test:unit` command on Node 26.8.1. No tests are skipped,
no timeout is raised, and application source remains unchanged.

The earlier [source-separation report](source-separation/README.md) retains its
original failed evidence; these repairs are a subsequent tooling/test scope.
Raw test, lint and build evidence is retained locally under
`AGENTS/plans/frontend-test-fixes/evidence/`.

## Separate Runtime Finding

Calling `Socket.start()` while inactive creates a fake transport. A subsequent
`setSessionActive(true)` reaches `startRealSocket()`, whose existing non-null guard
keeps that fake transport. This differs from the method comment. The application
call sites reviewed for this repair activate before starting; the runtime transition
requires a separate source change and is not silently altered by a test repair.
