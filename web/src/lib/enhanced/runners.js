import EventBus from '@/event-bus';
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

const enhancedMethods = {
  isRunnerDeleting(runnerId) {
    return this.deletingRunnerIds.includes(runnerId);
  },
  isRunnerCacheCleaning(runner) {
    if (this.cacheCleaningRunnerIds.includes(runner.id)) {
      return true;
    }
    if (!runner.cleaning_requested) {
      return false;
    }
    return !runner.touched
        || new Date(runner.cleaning_requested).getTime() >= new Date(runner.touched).getTime();
  },
  runnerLifecycleState(runner) {
    if (this.isRunnerDeleting(runner.id)) {
      return 'deleting';
    }
    if (this.isRunnerCacheCleaning(runner)) {
      return 'cache-cleaning';
    }
    if (!runner.registered) {
      return 'pending';
    }
    if (!runner.active) {
      return 'inactive';
    }
    return 'registered';
  },
  runnerLifecycleLabel(runner) {
    return {
      pending: 'runnerPending',
      registered: 'runnerRegistered',
      inactive: 'runnerInactive',
      deleting: 'runnerDeleting',
      'cache-cleaning': 'runnerCacheCleaning',
    }[this.runnerLifecycleState(runner)];
  },
  runnerLifecycleColor(runner) {
    return {
      pending: 'warning',
      registered: 'success',
      inactive: 'blue-grey lighten-3',
      deleting: 'error',
      'cache-cleaning': 'info',
    }[this.runnerLifecycleState(runner)];
  },
  lifecycleErrorMessage(error) {
    const payload = error.response && error.response.data;
    if (payload && payload.error === 'PROJECT_RUNNER_ASSIGNMENTS_ACTIVE') {
      const assignments = (payload.assignments || [])
        .map((assignment) => `#${assignment.task_id} (${assignment.status})`)
        .join(', ');
      return this.$t('runnerAssignmentConflict', { assignments });
    }
    return getErrorMessage(error);
  },
  async deleteItem(itemId) {
    this.itemId = itemId;
    const item = this.items.find((runner) => runner.id === itemId);
    if (!this.deletingRunnerIds.includes(itemId)) {
      this.deletingRunnerIds.push(itemId);
    }
    try {
      await axios({
        method: 'delete',
        url: this.getSingleItemUrl(),
        responseType: 'json',
      });
      EventBus.$emit(this.getEventName(), { action: 'delete', item });
      await this.loadItems();
    } catch (error) {
      EventBus.$emit('i-snackbar', {
        color: 'error',
        text: this.lifecycleErrorMessage(error),
      });
    } finally {
      this.deletingRunnerIds = this.deletingRunnerIds.filter((id) => id !== itemId);
    }
  },
};

export default enhancedMethods;
