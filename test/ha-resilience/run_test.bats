#!/usr/bin/env bats

setup() {
  script="${BATS_TEST_DIRNAME}/run.sh"
}

@test "help documents the required image contract" {
  run "${script}" --help
  [ "${status}" -eq 0 ]
  [[ "${output}" == *"SEMAPHORE_HA_REPLACEMENT_IMAGE"* ]]
  [[ "${output}" == *"Machine-readable JSON report"* ]]
}

@test "missing image input fails before topology mutation" {
  run env -u SEMAPHORE_HA_SERVER_A_IMAGE -u SEMAPHORE_HA_SERVER_B_IMAGE \
    -u SEMAPHORE_HA_REPLACEMENT_IMAGE -u SEMAPHORE_HA_RUNNER_IMAGE \
    "${script}"
  [ "${status}" -eq 1 ]
  [[ "${output}" == *"SEMAPHORE_HA_SERVER_A_IMAGE"* ]]
}

@test "proxy abandons an unresponsive node before the QA client times out" {
  run grep -F "proxy_read_timeout 2s;" "${BATS_TEST_DIRNAME}/nginx.conf"
  [ "${status}" -eq 0 ]

  run grep -F "proxy_next_upstream_tries 4;" "${BATS_TEST_DIRNAME}/nginx.conf"
  [ "${status}" -eq 0 ]

  run grep -F "server server-a:3000 max_fails=1 fail_timeout=1s;" "${BATS_TEST_DIRNAME}/nginx.conf"
  [ "${status}" -eq 0 ]

  run grep -F "server server-b:3000 max_fails=1 fail_timeout=1s;" "${BATS_TEST_DIRNAME}/nginx.conf"
  [ "${status}" -eq 0 ]

  run grep -F " resolve" "${BATS_TEST_DIRNAME}/nginx.conf"
  [ "${status}" -eq 1 ]
}

@test "proxy presents the internal server web root as a browser-relative base" {
  run grep -F "sub_filter 'http://proxy:8080/' '/';" "${BATS_TEST_DIRNAME}/nginx.conf"
  [ "${status}" -eq 0 ]
}
