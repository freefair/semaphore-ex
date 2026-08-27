import axios from 'axios';

export const enhancedComputed = {
  runnerIdentity() {
    const id = this.item?.used_runner_id;
    const name = this.item?.used_runner_name;
    if (id == null) return name || '';
    return name ? `#${id} — ${name}` : `#${id}`;
  },
};

export const enhancedMethods = {
  runnerAttemptIdentity(attempt) {
    if (attempt.runner_id == null) return attempt.runner_name || '—';
    return attempt.runner_name
      ? `#${attempt.runner_id} — ${attempt.runner_name}`
      : `#${attempt.runner_id}`;
  },
  runnerAttemptLabel(outcome) {
    const labels = {
      active: 'Active',
      requeued: 'Requeued',
      succeeded: 'Succeeded',
      failed: 'Failed',
      stopped: 'Stopped',
    };
    return labels[outcome] || outcome;
  },
  runnerAttemptColor(outcome) {
    const colors = {
      active: 'info',
      requeued: 'warning',
      succeeded: 'success',
      failed: 'error',
      stopped: 'grey darken-1',
    };
    return colors[outcome] || 'grey';
  },
  async loadRunnerAttempts(taskId) {
    if (taskId == null) return { attempts: [], error: null };
    try {
      const { data } = await axios.get(
        `/api/project/${this.projectId}/tasks/${taskId}/runner-attempts`,
      );
      return { attempts: data || [], error: null };
    } catch {
      return {
        attempts: [],
        error: 'Runner attempt history could not be loaded.',
      };
    }
  },
};
