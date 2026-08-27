package db

// ResolveExecutorImage validates persisted legacy data at the task-start boundary.
func (tpl *Template) ResolveExecutorImage() (*string, error) {
	if tpl.ExecutorImage == nil {
		return nil, nil
	}
	return NormalizeExecutorImage(*tpl.ExecutorImage)
}

// EffectiveRunnerTags returns the multi-tag policy with a legacy single-tag fallback.
func (tpl Template) EffectiveRunnerTags() []string {
	if tags := NormalizeRunnerTags(tpl.RunnerTags); len(tags) > 0 {
		return tags
	}
	if tpl.RunnerTag != nil {
		return NormalizeRunnerTags([]string{*tpl.RunnerTag})
	}
	return nil
}

// EffectiveRunnerTagMatchMode returns the validated match mode default.
func (tpl Template) EffectiveRunnerTagMatchMode() RunnerTagMatchMode {
	if tpl.RunnerTagMatchMode == RunnerTagMatchAny {
		return RunnerTagMatchAny
	}
	return RunnerTagMatchAll
}
