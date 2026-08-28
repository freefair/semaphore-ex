export const enhancedComputed = {
  elapsedTime() {
    if (!this.details) return '';
    const { run } = this.details;
    return this.formatElapsed(run.start || run.created, run.end);
  },
};

export const enhancedMethods = {
  normalizeNodeStatus(status) {
    switch (status) {
      case 'succeeded': return 'success';
      case 'queued': return 'waiting';
      default: return status;
    }
  },
  isActiveRunStatus(status) {
    return ['pending', 'queued', 'running', 'approval'].includes(status);
  },
  formatElapsed(start, end) {
    if (!start) return '';
    const finishedAt = new Date(end || Date.now()).getTime();
    const duration = Math.max(0, finishedAt - new Date(start).getTime());
    const totalSeconds = Math.floor(duration / 1000);
    const minutes = Math.floor(totalSeconds / 60);
    const seconds = totalSeconds % 60;
    return minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;
  },
  onWebsocketDataReceived(data) {
    if (data.type !== 'update' || data.project_id !== this.projectId) return;
    const belongsToRun = (this.details?.nodes || []).some(
      (node) => node.task && node.task.id === data.task_id,
    );
    if (belongsToRun) this.loadData();
  },
};
