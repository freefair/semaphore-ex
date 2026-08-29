#!/usr/bin/env bats

@test "clean-room build supplies every VCS-derived build input" {
  dockerfile="${BATS_TEST_DIRNAME}/../../deployment/docker/server/Dockerfile"
  script="${BATS_TEST_DIRNAME}/build-clean-room-images.sh"

  for argument in SOURCE_DATE_EPOCH SOURCE_TAG SOURCE_SHA; do
    run grep -F -- "--build-arg ${argument}=" "${script}"
    [ "${status}" -eq 0 ]

    run grep -F -- "ARG ${argument}" "${dockerfile}"
    [ "${status}" -eq 0 ]
  done

  run grep -F -- 'CORE_REVISION="${core_revision}"' "${dockerfile}"
  [ "${status}" -eq 0 ]
  run grep -F -- 'SOURCE_DATE_EPOCH="${source_date_epoch}"' "${dockerfile}"
  [ "${status}" -eq 0 ]
  run grep -F -- 'SEMAPHORE_SOURCE_TAG="${source_tag}"' "${dockerfile}"
  [ "${status}" -eq 0 ]
  run grep -F -- 'SEMAPHORE_SOURCE_SHA="${source_sha}"' "${dockerfile}"
  [ "${status}" -eq 0 ]
  taskfile="${BATS_TEST_DIRNAME}/../../taskfile.yml"
  run grep -F -- '${SEMAPHORE_SOURCE_TAG:-}' "${taskfile}"
  [ "${status}" -eq 0 ]
  run grep -F -- '${SEMAPHORE_SOURCE_SHA:-}' "${taskfile}"
  [ "${status}" -eq 0 ]
  run grep -F -- 'task build:be \' "${dockerfile}"
  [ "${status}" -eq 0 ]
}

@test "skew fixture rebuilds the embedded frontend" {
  dockerfile="${BATS_TEST_DIRNAME}/Dockerfile.skew"

  run grep -F -- 'AS frontend-builder' "${dockerfile}"
  [ "${status}" -eq 0 ]
  run grep -F -- 'VUE_APP_OUTPUT_DIR=/out/public' "${dockerfile}"
  [ "${status}" -eq 0 ]
  run grep -F -- 'COPY --from=frontend-builder /out/public ./api/public' "${dockerfile}"
  [ "${status}" -eq 0 ]
}
