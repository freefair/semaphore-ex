{{ if .Mysql }}drop index `project__deployment_window_decision__project_created` on `project__deployment_window_decision`{{ else }}drop index `project__deployment_window_decision__project_created`{{ end }};
drop table `project__deployment_window_decision`;
{{ if .Mysql }}drop index `project__deployment_window_rule__policy` on `project__deployment_window_rule`{{ else }}drop index `project__deployment_window_rule__policy`{{ end }};
drop table `project__deployment_window_rule`;
drop table `project__deployment_window_policy`;
