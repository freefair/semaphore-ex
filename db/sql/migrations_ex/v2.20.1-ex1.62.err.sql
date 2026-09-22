{{ if .Mysql }}drop index `project__workflow_run_node__policy_guardrail_evaluation` on `project__workflow_run_node`{{ else }}drop index `project__workflow_run_node__policy_guardrail_evaluation`{{ end }};
alter table `project__workflow_run_node` drop column `policy_guardrail_evaluation_id`;
