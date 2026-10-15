<template>
  <EditDialog
    v-model="dialog"
    :save-button-text="saveButtonText"
    :save-disabled="!executionReady"
    :title="$t('newTask')"
    @save="closeDialog"
    @close="closeDialog"
    test-id="newTaskDialog"
    content-class="execution-preflight-task-dialog"
    :max-width="1200"
  >
    <template v-slot:title={}>
      <v-icon small class="execution-task-icon">
        {{ TEMPLATE_TYPE_ICONS[template?.type || ''] }}
      </v-icon>
      <span class="execution-task-title">{{ templateTitle }}</span>
      <v-icon class="execution-task-separator">mdi-chevron-right</v-icon>
      <span class="execution-task-action">
        {{ $t(isInventoryRefresh ? 'hostsRefresh' : 'newTask') }}
      </span>
    </template>

    <template v-slot:form="{ onSave, onError, needSave, needReset }">
      <TaskForm
        v-if="dialog"
        :user-permissions="userPermissions"
        :is-admin="isAdmin"
        :project-id="projectId"
        item-id="new"
        :template="template"
        @save="onSave"
        @error="onError"
        @preflight="handlePreflight(onError)"
        @execution-ready="executionReady = $event"
        :need-save="needSave"
        :need-reset="needReset"
        :source-task="sourceTask"
        :inventory-refresh="isInventoryRefresh"
      />
    </template>
  </EditDialog>
</template>
<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/new-task-dialog';

import { TEMPLATE_TYPE_ACTION_TITLES, TEMPLATE_TYPE_ICONS } from '@/lib/constants';
import TaskForm from './TaskForm.vue';
import EditDialog from './EditDialog.vue';

import EventBus from '../event-bus';

export default {
  components: {
    TaskForm,
    EditDialog,
  },
  props: {
    userPermissions: Number,
    isAdmin: Boolean,
    value: Boolean,
    projectId: Number,
    template: Object,
    sourceTask: Object,
  },
  data() {
    return {
      dialog: false,
      TEMPLATE_TYPE_ACTION_TITLES,
      TEMPLATE_TYPE_ICONS,
      executionReady: false,
    };
  },
  watch: {
    async dialog(val) {
      this.$emit('input', val);
    },

    async value(val) {
      this.dialog = val;
      if (val) this.executionReady = false;
    },
  },

  computed: {
    ...enhancedComputed,
    isInventoryRefresh() { return this.sourceTask?.params?.inventory_refresh === true; },
    templateTitle() {
      return this.template?.name || '';
    },
  },

  methods: {
    ...enhancedMethods,
    closeDialog(e) {
      this.dialog = false;
      this.executionReady = false;
      if (e) {
        EventBus.$emit('i-show-task', {
          taskId: e.item.id,
        });
        this.$emit('save', e);
      }
      this.$emit('close');
    },
  },
};
</script>

<style>
.execution-preflight-task-dialog > .v-card {
  display: flex;
  max-height: 90vh;
  flex-direction: column;
}

.execution-preflight-task-dialog > .v-card > .v-card__text {
  overflow-y: auto;
  min-height: 0;
}

.execution-preflight-task-dialog > .v-card > .v-card__title {
  flex-wrap: nowrap;
  flex-shrink: 0;
  white-space: normal;
  gap: 8px;
}

.execution-preflight-task-dialog .execution-task-title {
  min-width: 0;
  word-break: normal;
  overflow-wrap: anywhere;
}

.execution-preflight-task-dialog .execution-task-action {
  flex-shrink: 0;
}

.execution-preflight-task-dialog .item-dialog__title-actions {
  position: static;
  flex-shrink: 0;
}

.execution-preflight-task-dialog > .v-card > .v-card__actions {
  flex-shrink: 0;
}

@media (max-width: 599px) {
  .execution-preflight-task-dialog > .v-card > .v-card__title {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: start;
    margin-right: 0;
  }

  .execution-preflight-task-dialog .execution-task-title {
    grid-column: 2;
  }

  .execution-preflight-task-dialog .execution-task-icon {
    margin-top: 6px;
  }

  .execution-preflight-task-dialog .execution-task-action {
    grid-column: 2;
    grid-row: 2;
  }

  .execution-preflight-task-dialog .item-dialog__title-actions {
    grid-column: 3;
    grid-row: 1;
  }

  .execution-preflight-task-dialog .execution-task-separator,
  .execution-preflight-task-dialog .v-card__title > .spacer {
    display: none;
  }
}
</style>
