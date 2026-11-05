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

Vitest runs the component suite in jsdom using the Vue plugin from the Vite production build. The test setup installs Vuetify components and directives, the application formatters and browser API fixtures. The production build writes the ordinary frontend to `api/public`.

## Component and Socket Contracts

Argument-picker stubs forward input and keyboard events through Vue component
emissions, matching the component event boundary used by Vuetify. The tests cover
trimmed input, Enter submission, form submission, rejected validation and unchanged
caller-owned arrays. The shared test setup registers Vuetify through Vue Test Utils. Individual mounts can provide a fresh instance for viewport-specific behavior.

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
