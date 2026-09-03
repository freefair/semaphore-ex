#!/usr/bin/env bats

setup() {
  workflow="${BATS_TEST_DIRNAME}/../../.github/workflows/product_build.yml"
}

@test "product workflow contains an independent required HA contract gate" {
  run grep -F "ha-resilience:" "${workflow}"
  [ "${status}" -eq 0 ]

  run grep -F "./test/ha-resilience/build-clean-room-images.sh" "${workflow}"
  [ "${status}" -eq 0 ]

  run grep -F "./test/ha-resilience/run.sh --report dist/ha-resilience/report.json" "${workflow}"
  [ "${status}" -eq 0 ]
}

@test "HA gate retains and validates its machine-readable report" {
  run grep -F "jq --exit-status '.result == \"passed\"' dist/ha-resilience/report.json" "${workflow}"
  [ "${status}" -eq 0 ]

  run grep -F 'name: ha-resilience-${{ github.sha }}' "${workflow}"
  [ "${status}" -eq 0 ]

  run grep -F "retention-days: 14" "${workflow}"
  [ "${status}" -eq 0 ]
}
