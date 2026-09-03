{{ if .Mysql }}drop index `policy_guardrail_evaluation__project_created` on `policy_guardrail_evaluation`{{ else }}drop index `policy_guardrail_evaluation__project_created`{{ end }};
drop table `policy_guardrail_evaluation`;
{{ if .Mysql }}drop index `policy_guardrail_revision__scope` on `policy_guardrail_revision`{{ else }}drop index `policy_guardrail_revision__scope`{{ end }};
drop table `policy_guardrail_revision`;
{{ if .Mysql }}drop index `policy_guardrail_draft__project` on `policy_guardrail_draft`{{ else }}drop index `policy_guardrail_draft__project`{{ end }};
drop table `policy_guardrail_draft`;
