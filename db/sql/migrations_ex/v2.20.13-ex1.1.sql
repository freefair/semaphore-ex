-- Upstream added survey fields after EX shipped 2.20.13. Preserve that
-- migration's identity and bytes while applying the later schema additively.
alter table `project__workflow_revision` add column `survey_vars` text null;
alter table `project__workflow_node` add column `survey_inputs` text null;
alter table `project__workflow_run` add column `environment` text null;
