# HA recovery investigation

## Question

Determine whether the source-separation changes introduced the node-kill HA
failure that reported extra runner attempts and no generation-2 terminal task.

## Observations

| Run | Image set | Retained | Node-kill result |
| --- | --- | --- | --- |
| Initial candidate | separation candidate/skew | no | Failed: 3 attempts across 2 expected generations; generation 2 not terminal |
| Exact baseline | `28f60c45` candidate/skew | no | Failed: 4 attempts across 2 expected generations; generation 2 not terminal |
| Candidate capture | separation candidate/skew | yes | Passed: 2 attempts across generations 1 and 2; generation 2 terminal |
| Baseline capture | `28f60c45` candidate/skew | yes | Passed: 2 attempts across generations 1 and 2; generation 2 terminal |

The retained candidate report is `ha-candidate-keep-report.json`; the
retained baseline report is `ha-baseline-keep-report.json`.

The retained baseline passing evidence was queried before cleanup:

| task status | assignment generation | runner ID | recovery reason |
| --- | ---: | ---: | --- |
| success | 2 | 1 | original execution is absent |

| attempt ID | generation | outcome | reason |
| ---: | ---: | --- | --- |
| 1 | 1 | requeued | previous task-control owner expired |
| 2 | 2 | succeeded | |

Filtered recovery excerpts are retained as
`ha-baseline-keep-server-a-recovery.log` and
`ha-baseline-keep-server-b-recovery.log`. The latter records the generation-2
dispatch and success; the former has no matching post-restart recovery lines.

The retained candidate task/attempt metadata was:

| task status | assignment generation | runner ID | recovery reason |
| --- | ---: | ---: | --- |
| error | 2 | 1 | original execution is absent |

| attempt ID | generation | outcome | reason |
| ---: | ---: | --- | --- |
| 1 | 1 | requeued | previous task-control owner expired |
| 2 | 2 | failed | original execution is absent |

`task__runner_attempt` has a unique `(task_id, generation)` constraint. A
failed count above two therefore cannot be duplicate inserts for generations
one and two; it indicates later assignment generations. The failed runs were
not retained, so their exact rows and logs are unavailable.

## Source comparison

The committed source diff from `28f60c45` to the separation commit relocates
the task start claim, runner-security metadata construction, and router
constructor wiring. It does not alter the `ClaimTaskStart` SQL predicate, the
assignment-generation increment, runner-attempt insert/update SQL, or recovery
transitions. The exact baseline reproduced the failure, so current evidence
does not attribute it to the separation.

## Conclusion

The behavior is nondeterministic and also occurs with the unchanged baseline images. The evidence is
insufficient to identify its root cause or to conclude that the harness is the
only contributor. No source, fixture, timeout, or gate changes were made.

## Cleanup

The passing baseline Compose environment was removed after the report, task
rows, attempt rows, and filtered logs were retained. Images and all evidence
files remain available under `/tmp/semaphore-separation-2`.
