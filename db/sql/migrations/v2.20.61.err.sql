{{ if .Mysql }}drop index `project__workflow_run_node__deployment_window_decision` on `project__workflow_run_node`{{ else }}drop index `project__workflow_run_node__deployment_window_decision`{{ end }};
alter table `project__workflow_run_node` drop column `blocked_at`;
alter table `project__workflow_run_node` drop column `next_eligible_known`;
alter table `project__workflow_run_node` drop column `next_eligible_at`;
alter table `project__workflow_run_node` drop column `deployment_window_decision_id`;

{{ if .Mysql }}drop index `project__workflow_trigger_invocation__decision` on `project__workflow_trigger_invocation`{{ else }}drop index `project__workflow_trigger_invocation__decision`{{ end }};
alter table `project__workflow_trigger_invocation` drop column `blocked_at`;
alter table `project__workflow_trigger_invocation` drop column `next_eligible_known`;
alter table `project__workflow_trigger_invocation` drop column `next_eligible_at`;
alter table `project__workflow_trigger_invocation` drop column `deployment_window_decision_id`;

{{ if .Mysql }}drop index `cluster__schedule_occurrence__decision` on `cluster__schedule_occurrence`{{ else }}drop index `cluster__schedule_occurrence__decision`{{ end }};
alter table `cluster__schedule_occurrence` drop column `blocked_at`;
alter table `cluster__schedule_occurrence` drop column `next_eligible_known`;
alter table `cluster__schedule_occurrence` drop column `next_eligible_at`;
alter table `cluster__schedule_occurrence` drop column `deployment_window_decision_id`;
alter table `cluster__schedule_occurrence` drop column `terminal_outcome`;
