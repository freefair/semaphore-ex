# Third-Party Licenses

Semaphore EX is built on the work of many open-source projects. This document identifies every third-party component distributed with Semaphore EX, in compliance with the attribution requirements of the respective licenses.

_Generated on **2026-11-13 11:13 UTC** by `scripts/collect_licenses.sh`._

To regenerate this file, run:

```bash
scripts/collect_licenses.sh
scripts/check_policy.py .licenses-cache/
scripts/generate_md.py .licenses-cache/ > THIRD-PARTY-LICENSES.md
```

## Summary
This document lists **191** third-party components distributed with Semaphore EX, grouped by ecosystem.

| Ecosystem | Components |
|-----------|------------|
| Go (backend) | 71 |
| npm (frontend) | 120 |

### License distribution

| License | Count |
|---------|-------|
| MIT | 141 |
| BSD-3-Clause | 25 |
| Apache-2.0 | 15 |
| MPL-2.0 | 4 |
| BSD-2-Clause | 4 |
| Python-2.0 | 1 |
| ISC | 1 |

## Go Backend Dependencies
Modules statically linked into the Semaphore EX server binary. Sourced from `go.mod` (production dependencies only).

| Component | Version(s) | License | Source |
|-----------|------------|---------|--------|
| `dario.cat/mergo` | v1.0.2 | BSD-3-Clause | [link](https://github.com/imdario/mergo/blob/v1.0.2/LICENSE) |
| `filippo.io/edwards25519` | v1.2.0 | BSD-3-Clause | [link](https://github.com/FiloSottile/edwards25519/blob/v1.2.0/LICENSE) |
| `github.com/Azure/go-ntlmssp` | v0.1.1 | MIT | [link](https://github.com/Azure/go-ntlmssp/blob/v0.1.1/LICENSE) |
| `github.com/beorn7/perks` | v1.0.1 | MIT | [link](https://github.com/beorn7/perks/blob/v1.0.1/LICENSE) |
| `github.com/boombuler/barcode` | v1.1.0 | MIT | [link](https://github.com/boombuler/barcode/blob/v1.1.0/LICENSE) |
| `github.com/cespare/xxhash/v2` | v2.3.0 | MIT | [link](https://github.com/cespare/xxhash/blob/v2.3.0/LICENSE.txt) |
| `github.com/cloudflare/circl` | v1.6.3 | BSD-3-Clause | [link](https://github.com/cloudflare/circl/blob/v1.6.3/LICENSE) |
| `github.com/coreos/go-oidc/v3` | v3.21.0 | Apache-2.0 | [link](https://github.com/coreos/go-oidc/blob/v3.21.0/LICENSE) |
| `github.com/creack/pty` | v1.1.24 | MIT | [link](https://github.com/creack/pty/blob/v1.1.24/LICENSE) |
| `github.com/cyphar/filepath-securejoin` | v0.6.1 | MPL-2.0 | [link](https://github.com/cyphar/filepath-securejoin/blob/v0.6.1/COPYING.md) |
| `github.com/dustin/go-humanize` | v1.0.1 | MIT | [link](https://github.com/dustin/go-humanize/blob/v1.0.1/LICENSE) |
| `github.com/emirpasic/gods` | v1.18.1 | BSD-2-Clause | [link](https://github.com/emirpasic/gods/blob/v1.18.1/LICENSE) |
| `github.com/felixge/httpsnoop` | v1.1.0 | MIT | [link](https://github.com/felixge/httpsnoop/blob/v1.1.0/LICENSE.txt) |
| `github.com/go-asn1-ber/asn1-ber` | v1.5.8 | MIT | [link](https://github.com/go-asn1-ber/asn1-ber/blob/v1.5.8/LICENSE) |
| `github.com/go-git/gcfg` | v1.5.1-0.20230307220236-3a3c6141e376 | BSD-3-Clause | [link](https://github.com/go-git/gcfg/blob/3a3c6141e376/LICENSE) |
| `github.com/go-git/go-billy/v5` | v5.9.0 | Apache-2.0 | [link](https://github.com/go-git/go-billy/blob/v5.9.0/LICENSE) |
| `github.com/go-git/go-git/v5` | v5.19.2 | Apache-2.0 | [link](https://github.com/go-git/go-git/blob/v5.19.2/LICENSE) |
| `github.com/go-gorp/gorp/v3` | v3.1.0 | MIT | [link](https://github.com/go-gorp/gorp/blob/v3.1.0/LICENSE) |
| `github.com/go-jose/go-jose/v4` | v4.1.6 | Apache-2.0 | [link](https://github.com/go-jose/go-jose/blob/v4.1.6/LICENSE) |
| `github.com/go-ldap/ldap/v3` | v3.4.14 | MIT | [link](https://github.com/go-ldap/ldap/blob/v3.4.14/v3/LICENSE) |
| `github.com/go-sql-driver/mysql` | v1.10.0 | MPL-2.0 | [link](https://github.com/go-sql-driver/mysql/blob/v1.10.0/LICENSE) |
| `github.com/golang/groupcache` | v0.0.0-20241129210726-2c02b8208cf8 | Apache-2.0 | [link](https://github.com/golang/groupcache/blob/2c02b8208cf8/LICENSE) |
| `github.com/google/go-github` | v17.0.0+incompatible | BSD-3-Clause | [link](https://github.com/google/go-github/blob/v17.0.0/LICENSE) |
| `github.com/google/go-querystring` | v1.2.0 | BSD-3-Clause | [link](https://github.com/google/go-querystring/blob/v1.2.0/LICENSE) |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause | [link](https://github.com/google/uuid/blob/v1.6.0/LICENSE) |
| `github.com/gorilla/handlers` | v1.5.2 | BSD-3-Clause | [link](https://github.com/gorilla/handlers/blob/v1.5.2/LICENSE) |
| `github.com/gorilla/mux` | v1.8.1 | BSD-3-Clause | [link](https://github.com/gorilla/mux/blob/v1.8.1/LICENSE) |
| `github.com/gorilla/securecookie` | v1.1.2 | BSD-3-Clause | [link](https://github.com/gorilla/securecookie/blob/v1.1.2/LICENSE) |
| `github.com/gorilla/websocket` | v1.5.4-0.20250319132907-e064f32e3674 | BSD-2-Clause | [link](https://github.com/gorilla/websocket/blob/e064f32e3674/LICENSE) |
| `github.com/jbenet/go-context` | v0.0.0-20150711004518-d14ea06fba99 | MIT | [link](https://github.com/jbenet/go-context/blob/d14ea06fba99/LICENSE) |
| `github.com/kevinburke/ssh_config` | v1.6.0 | MIT | [link](https://github.com/kevinburke/ssh_config/blob/v1.6.0/LICENSE) |
| `github.com/klauspost/cpuid/v2` | v2.3.0 | MIT | [link](https://github.com/klauspost/cpuid/blob/v2.3.0/LICENSE) |
| `github.com/lann/builder` | v0.0.0-20180802200727-47ae307949d0 | MIT | [link](https://github.com/lann/builder/blob/47ae307949d0/LICENSE) |
| `github.com/lann/ps` | v0.0.0-20150810152359-62de8c46ede0 | MIT | [link](https://github.com/lann/ps/blob/62de8c46ede0/LICENSE) |
| `github.com/lib/pq` | v1.12.3 | MIT | [link](https://github.com/lib/pq/blob/v1.12.3/LICENSE) |
| `github.com/Masterminds/squirrel` | v1.5.4 | MIT | [link](https://github.com/Masterminds/squirrel/blob/v1.5.4/LICENSE) |
| `github.com/mattn/go-isatty` | v0.0.24 | MIT | [link](https://github.com/mattn/go-isatty/blob/v0.0.24/LICENSE) |
| `github.com/munnerz/goautoneg` | v0.0.0-20191010083416-a7dc8b61c822 | BSD-3-Clause | [link](https://github.com/munnerz/goautoneg/blob/a7dc8b61c822/LICENSE) |
| `github.com/ncruces/go-strftime` | v1.0.0 | MIT | [link](https://github.com/ncruces/go-strftime/blob/v1.0.0/LICENSE) |
| `github.com/pjbgf/sha1cd` | v0.6.0 | Apache-2.0 | [link](https://github.com/pjbgf/sha1cd/blob/v0.6.0/LICENSE) |
| `github.com/pquerna/otp` | v1.5.0 | Apache-2.0 | [link](https://github.com/pquerna/otp/blob/v1.5.0/LICENSE) |
| `github.com/prometheus/client_golang` | v1.23.2 | BSD-3-Clause | [link](https://github.com/prometheus/client_golang/blob/v1.23.2/internal/github.com/golang/gddo/LICENSE) |
| `github.com/prometheus/client_model` | v0.6.2 | Apache-2.0 | [link](https://github.com/prometheus/client_model/blob/v0.6.2/LICENSE) |
| `github.com/prometheus/common` | v0.66.1 | Apache-2.0 | [link](https://github.com/prometheus/common/blob/v0.66.1/LICENSE) |
| `github.com/ProtonMail/go-crypto` | v1.4.1 | BSD-3-Clause | [link](https://github.com/ProtonMail/go-crypto/blob/v1.4.1/LICENSE) |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | [link](https://github.com/remyoudompheng/bigfft/blob/24d4a6f8daec/LICENSE) |
| `github.com/robfig/cron/v3` | v3.0.1 | MIT | [link](https://github.com/robfig/cron/blob/v3.0.1/LICENSE) |
| `github.com/sergi/go-diff` | v1.4.0 | MIT | [link](https://github.com/sergi/go-diff/blob/v1.4.0/LICENSE) |
| `github.com/sirupsen/logrus` | v1.10.2 | MIT | [link](https://github.com/sirupsen/logrus/blob/v1.10.2/LICENSE) |
| `github.com/skeema/knownhosts` | v1.3.2 | Apache-2.0 | [link](https://github.com/skeema/knownhosts/blob/v1.3.2/LICENSE) |
| `github.com/snikch/goodman` | v0.0.0-20171125024755-10e37e294daa | MIT | [link](https://github.com/snikch/goodman/blob/10e37e294daa/LICENSE) |
| `github.com/spf13/cobra` | v1.10.2 | Apache-2.0 | [link](https://github.com/spf13/cobra/blob/v1.10.2/LICENSE.txt) |
| `github.com/spf13/pflag` | v1.0.10 | BSD-3-Clause | [link](https://github.com/spf13/pflag/blob/v1.0.10/LICENSE) |
| `github.com/stretchr/testify` | v1.12.1 | MIT | [link](https://github.com/stretchr/testify/blob/v1.12.1/LICENSE) |
| `github.com/thedevsaddam/gojsonq/v2` | v2.5.2 | MIT | [link](https://github.com/thedevsaddam/gojsonq/blob/v2.5.2/LICENSE.md) |
| `github.com/xanzy/ssh-agent` | v0.3.3 | Apache-2.0 | [link](https://github.com/xanzy/ssh-agent/blob/v0.3.3/LICENSE) |
| `go.yaml.in/yaml/v2` | v2.4.2 | Apache-2.0 | [link](https://github.com/yaml/go-yaml/blob/v2.4.2/LICENSE) |
| `go.yaml.in/yaml/v3` | v3.0.5 | MIT | [link](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE) |
| `golang.org/x/crypto` | v0.58.0 | BSD-3-Clause | [link](https://cs.opensource.google/go/x/crypto/+/v0.58.0:LICENSE) |
| `golang.org/x/net` | v0.62.0 | BSD-3-Clause | [link](https://cs.opensource.google/go/x/net/+/v0.62.0:LICENSE) |
| `golang.org/x/oauth2` | v0.37.0 | BSD-3-Clause | [link](https://cs.opensource.google/go/x/oauth2/+/v0.37.0:LICENSE) |
| `golang.org/x/sys` | v0.49.0 | BSD-3-Clause | [link](https://cs.opensource.google/go/x/sys/+/v0.49.0:LICENSE) |
| `golang.org/x/term` | v0.47.0 | BSD-3-Clause | [link](https://cs.opensource.google/go/x/term/+/v0.47.0:LICENSE) |
| `google.golang.org/protobuf` | v1.36.8 | BSD-3-Clause | [link](https://github.com/protocolbuffers/protobuf-go/blob/v1.36.8/LICENSE) |
| `gopkg.in/natefinch/lumberjack.v2` | v2.2.1 | MIT | [link](https://github.com/natefinch/lumberjack/blob/v2.2.1/LICENSE) |
| `gopkg.in/warnings.v0` | v0.1.2 | BSD-2-Clause | [link](https://github.com/go-warnings/warnings/blob/v0.1.2/LICENSE) |
| `gopkg.in/yaml.v3` | v3.0.1 | MIT | [link](https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE) |
| `modernc.org/libc` | v1.75.7 | MIT | [link](https://gitlab.com/cznic/libc/blob/v1.75.7/LICENSE-3RD-PARTY.md) |
| `modernc.org/mathutil` | v1.7.1 | BSD-3-Clause | [link](https://gitlab.com/cznic/mathutil/-/blob/master/LICENSE) |
| `modernc.org/memory` | v1.12.1 | BSD-3-Clause | [link](https://gitlab.com/cznic/memory/blob/v1.12.1/LICENSE-GO) |
| `modernc.org/sqlite` | v1.59.0 | BSD-3-Clause | [link](https://gitlab.com/cznic/sqlite/blob/v1.59.0/LICENSE) |

## Frontend Dependencies (npm)
Packages bundled into the web UI assets, which are embedded in the server binary at build time. Sourced from the frontend `package.json` (production dependencies only; dev dependencies are not distributed).

| Component | Version(s) | License | Source |
|-----------|------------|---------|--------|
| `@babel/helper-string-parser` | 7.29.11 | MIT | [link](https://github.com/babel/babel) |
| `@babel/helper-validator-identifier` | 7.29.7 | MIT | [link](https://github.com/babel/babel) |
| `@babel/parser` | 7.29.11 | MIT | [link](https://github.com/babel/babel) |
| `@babel/types` | 7.29.11 | MIT | [link](https://github.com/babel/babel) |
| `@codemirror/autocomplete` | 6.20.4 | MIT | [link](git+https://code.haverbeke.berlin/codemirror/autocomplete) |
| `@codemirror/commands` | 6.11.1 | MIT | [link](git+https://code.haverbeke.berlin/codemirror/commands) |
| `@codemirror/lang-json` | 6.0.2 | MIT | [link](https://github.com/codemirror/lang-json) |
| `@codemirror/lang-yaml` | 6.1.3 | MIT | [link](https://github.com/codemirror/lang-yaml) |
| `@codemirror/language` | 6.13.1 | MIT | [link](git+https://code.haverbeke.berlin/codemirror/language) |
| `@codemirror/lint` | 6.9.7 | MIT | [link](git+https://code.haverbeke.berlin/codemirror/lint) |
| `@codemirror/search` | 6.7.2 | MIT | [link](git+https://code.haverbeke.berlin/codemirror/search) |
| `@codemirror/state` | 6.7.6 | MIT | [link](git+https://code.haverbeke.berlin/codemirror/state) |
| `@codemirror/streamparser` | 6.0.0 | MIT | [link](git+https://code.haverbeke.berlin/codemirror/streamparser) |
| `@codemirror/view` | 6.43.15 | MIT | [link](git+https://code.haverbeke.berlin/codemirror/view) |
| `@intlify/core-base` | 11.4.16 | MIT | [link](https://github.com/intlify/vue-i18n) |
| `@intlify/devtools-types` | 11.4.16 | MIT | [link](https://github.com/intlify/vue-i18n) |
| `@intlify/message-compiler` | 11.4.16 | MIT | [link](https://github.com/intlify/vue-i18n) |
| `@intlify/shared` | 11.4.16 | MIT | [link](https://github.com/intlify/vue-i18n) |
| `@jridgewell/sourcemap-codec` | 1.6.0 | MIT | [link](https://github.com/jridgewell/sourcemaps) |
| `@kurkle/color` | 0.3.4 | MIT | [link](https://github.com/kurkle/color) |
| `@lezer/common` | 1.5.4 | MIT | [link](git+https://code.haverbeke.berlin/lezer/common) |
| `@lezer/highlight` | 1.2.5 | MIT | [link](git+https://code.haverbeke.berlin/lezer/highlight) |
| `@lezer/json` | 1.0.3 | MIT | [link](https://github.com/lezer-parser/json) |
| `@lezer/lr` | 1.4.11 | MIT | [link](git+https://code.haverbeke.berlin/lezer/lr) |
| `@lezer/yaml` | 1.0.4 | MIT | [link](https://github.com/lezer-parser/yaml) |
| `@marijn/find-cluster-break` | 1.0.4 | MIT | [link](git+https://code.haverbeke.berlin/marijn/find-cluster-break) |
| `@mdi/font` | 7.4.47 | Apache-2.0 | [link](https://github.com/Templarian/MaterialDesign-Webfont) |
| `@oxc-project/types` | 0.155.0 | MIT | [link](https://github.com/oxc-project/oxc) |
| `@parcel/watcher` | 2.6.0 | MIT | [link](https://github.com/parcel-bundler/watcher) |
| `@parcel/watcher-darwin-arm64` | 2.6.0 | MIT | [link](https://github.com/parcel-bundler/watcher) |
| `@rolldown/binding-darwin-arm64` | 1.2.15 | MIT | [link](https://github.com/rolldown/rolldown) |
| `@rolldown/pluginutils` | 1.0.1 | MIT | [link](https://github.com/rolldown/plugins) |
| `@tanstack/virtual-core` | 3.19.8 | MIT | [link](https://github.com/TanStack/virtual) |
| `@tanstack/vue-virtual` | 3.13.49 | MIT | [link](https://github.com/TanStack/virtual) |
| `@types/sortablejs` | 1.15.9 | MIT | [link](https://github.com/DefinitelyTyped/DefinitelyTyped) |
| `@vue/compiler-core` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vue/compiler-dom` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vue/compiler-sfc` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vue/compiler-ssr` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vue/devtools-api` | 6.6.4 | MIT | [link](https://github.com/vuejs/vue-devtools) |
| `@vue/reactivity` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vue/runtime-core` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vue/runtime-dom` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vue/server-renderer` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vue/shared` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `@vuetify/loader-shared` | 2.1.2 | MIT | [link](https://github.com/vuetifyjs/vuetify-loader) |
| `@vuetify/v0` | 1.3.1 | MIT | [link](https://github.com/vuetifyjs/0) |
| `agent-base` | 6.0.2 | MIT | [link](https://github.com/TooTallNate/node-agent-base) |
| `ansi_up` | 6.0.6 | MIT | [link](https://github.com/drudru/ansi_up) |
| `argparse` | 2.0.1 | Python-2.0 | [link](https://github.com/nodeca/argparse) |
| `asynckit` | 0.4.0 | MIT | [link](https://github.com/alexindigo/asynckit) |
| `axios` | 1.20.0 | MIT | [link](https://github.com/axios/axios) |
| `call-bind-apply-helpers` | 1.0.2 | MIT | [link](https://github.com/ljharb/call-bind-apply-helpers) |
| `chart.js` | 4.6.1 | MIT | [link](https://github.com/chartjs/Chart.js) |
| `chokidar` | 5.0.0 | MIT | [link](https://github.com/paulmillr/chokidar) |
| `codemirror` | 6.0.2 | MIT | [link](https://github.com/codemirror/basic-setup) |
| `combined-stream` | 1.0.8 | MIT | [link](https://github.com/felixge/node-combined-stream) |
| `crelt` | 1.0.7 | MIT | [link](git+https://code.haverbeke.berlin/marijn/crelt) |
| `cron-parser` | 5.10.1 | MIT | [link](https://github.com/harrisiirak/cron-parser) |
| `csstype` | 3.2.3 | MIT | [link](https://github.com/frenic/csstype) |
| `dayjs` | 1.11.23 | MIT | [link](https://github.com/iamkun/dayjs) |
| `debug` | 4.4.3 | MIT | [link](https://github.com/debug-js/debug) |
| `delayed-stream` | 1.0.0 | MIT | [link](https://github.com/felixge/node-delayed-stream) |
| `detect-libc` | 2.1.2 | Apache-2.0 | [link](https://github.com/lovell/detect-libc) |
| `drawflow` | 0.0.60 | MIT | [link](https://github.com/jerosoler/Drawflow) |
| `dunder-proto` | 1.0.1 | MIT | [link](https://github.com/es-shims/dunder-proto) |
| `entities` | 7.0.1 | BSD-2-Clause | [link](https://github.com/fb55/entities) |
| `es-define-property` | 1.0.1 | MIT | [link](https://github.com/ljharb/es-define-property) |
| `es-errors` | 1.3.0 | MIT | [link](https://github.com/ljharb/es-errors) |
| `es-object-atoms` | 1.1.2 | MIT | [link](https://github.com/ljharb/es-object-atoms) |
| `es-set-tostringtag` | 2.1.0 | MIT | [link](https://github.com/es-shims/es-set-tostringtag) |
| `estree-walker` | 2.0.2 | MIT | [link](https://github.com/Rich-Harris/estree-walker) |
| `fdir` | 6.5.0 | MIT | [link](https://github.com/thecodrr/fdir) |
| `follow-redirects` | 1.16.2 | MIT | [link](https://github.com/follow-redirects/follow-redirects) |
| `form-data` | 4.0.6 | MIT | [link](https://github.com/form-data/form-data) |
| `fsevents` | 2.3.3 | MIT | [link](https://github.com/fsevents/fsevents) |
| `function-bind` | 1.1.2 | MIT | [link](https://github.com/Raynos/function-bind) |
| `get-intrinsic` | 1.3.0 | MIT | [link](https://github.com/ljharb/get-intrinsic) |
| `get-proto` | 1.0.1 | MIT | [link](https://github.com/ljharb/get-proto) |
| `gopd` | 1.2.0 | MIT | [link](https://github.com/ljharb/gopd) |
| `has-symbols` | 1.1.0 | MIT | [link](https://github.com/inspect-js/has-symbols) |
| `has-tostringtag` | 1.0.2 | MIT | [link](https://github.com/inspect-js/has-tostringtag) |
| `hasown` | 2.0.4 | MIT | [link](https://github.com/inspect-js/hasOwn) |
| `https-proxy-agent` | 5.0.1 | MIT | [link](https://github.com/TooTallNate/node-https-proxy-agent) |
| `immutable` | 5.1.9 | MIT | [link](https://github.com/immutable-js/immutable-js) |
| `is-extglob` | 2.1.1 | MIT | [link](https://github.com/jonschlinkert/is-extglob) |
| `is-glob` | 4.0.3 | MIT | [link](https://github.com/micromatch/is-glob) |
| `js-yaml` | 5.4.2 | MIT | [link](https://github.com/nodeca/js-yaml) |
| `lightningcss` | 1.33.0 | MPL-2.0 | [link](https://github.com/parcel-bundler/lightningcss) |
| `lightningcss-darwin-arm64` | 1.33.0 | MPL-2.0 | [link](https://github.com/parcel-bundler/lightningcss) |
| `luxon` | 3.7.2 | MIT | [link](https://github.com/moment/luxon) |
| `magic-string` | 0.30.21 | MIT | [link](https://github.com/Rich-Harris/magic-string) |
| `math-intrinsics` | 1.1.0 | MIT | [link](https://github.com/es-shims/math-intrinsics) |
| `mime-db` | 1.52.0 | MIT | [link](https://github.com/jshttp/mime-db) |
| `mime-types` | 2.1.35 | MIT | [link](https://github.com/jshttp/mime-types) |
| `ms` | 2.1.3 | MIT | [link](https://github.com/vercel/ms) |
| `nanoid` | 3.3.20 | MIT | [link](https://github.com/ai/nanoid) |
| `node-addon-api` | 7.1.1 | MIT | [link](https://github.com/nodejs/node-addon-api) |
| `picocolors` | 1.1.1 | ISC | [link](https://github.com/alexeyraspopov/picocolors) |
| `picomatch` | 4.0.7 | MIT | [link](https://github.com/micromatch/picomatch) |
| `postcss` | 8.5.32 | MIT | [link](https://github.com/postcss/postcss) |
| `proxy-from-env` | 2.1.0 | MIT | [link](https://github.com/Rob--W/proxy-from-env) |
| `readdirp` | 5.1.1 | MIT | [link](https://github.com/paulmillr/readdirp) |
| `rolldown` | 1.2.15 | MIT | [link](https://github.com/rolldown/rolldown) |
| `sass` | 1.106.1 | MIT | [link](https://github.com/sass/dart-sass) |
| `source-map-js` | 1.2.2 | BSD-3-Clause | [link](https://github.com/7rulnik/source-map-js) |
| `style-mod` | 4.1.4 | MIT | [link](git+https://code.haverbeke.berlin/marijn/style-mod) |
| `tinyglobby` | 0.2.17 | MIT | [link](https://github.com/SuperchupuDev/tinyglobby) |
| `upath` | 2.0.1 | MIT | [link](https://github.com/anodynos/upath) |
| `vite` | 8.3.5 | MIT | [link](https://github.com/vitejs/vite) |
| `vite-plugin-vuetify` | 2.1.3 | MIT | [link](https://github.com/vuetifyjs/vuetify-loader) |
| `vue` | 3.5.44 | MIT | [link](https://github.com/vuejs/core) |
| `vue-chartjs` | 5.3.4 | MIT | [link](https://github.com/apertureless/vue-chartjs) |
| `vue-codemirror6` | 1.7.0 | MIT | [link](https://github.com/logue/vue-codemirror6) |
| `vue-demi` | 0.14.10 | MIT | [link](https://github.com/antfu/vue-demi) |
| `vue-draggable-plus` | 0.6.1 | MIT | [link](https://github.com/Alfred-Skyblue/vue-draggable-plus) |
| `vue-i18n` | 11.4.16 | MIT | [link](https://github.com/intlify/vue-i18n) |
| `vue-router` | 4.6.4 | MIT | [link](https://github.com/vuejs/router) |
| `vuetify` | 4.3.1 | MIT | [link](https://github.com/vuetifyjs/vuetify) |
| `w3c-keyname` | 2.2.8 | MIT | [link](https://github.com/marijnh/w3c-keyname) |

---

## License texts

Full license texts for each component are available at the source URLs listed above. For permissively-licensed packages (MIT, BSD, ISC, Apache-2.0), the original LICENSE and NOTICE files are preserved in their respective package directories within the Semaphore EX distribution.

If you believe a component is missing from this list or incorrectly attributed, please open an issue at https://github.com/freefair/semaphore-ex/issues.

<!-- end of generated file -->
