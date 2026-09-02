<template>
  <EditDialog
    v-model="dialog"
    :save-button-text="saveButtonText"
    :title="$t('newTask')"
    @save="closeDialog"
    @close="closeDialog"
    test-id="newTaskDialog"
    content-class="execution-preflight-task-dialog"
  >
    <template v-slot:title={}>
      <v-icon small class="mr-4">{{ TEMPLATE_TYPE_ICONS[template?.type || ''] }}</v-icon>
      <span class="breadcrumbs__item">{{ templateTitle }}</span>
      <v-icon>mdi-chevron-right</v-icon>
      <span class="breadcrumbs__item">{{ $t('newTask') }}</span>
    </template>

    <template v-slot:form="{ onSave, onError, needSave, needReset }">
      <TaskForm
        :project-id="projectId"
        item-id="new"
        :template="template"
        @save="onSave"
        @error="onError"
        @preflight="handlePreflight(onError)"
        :need-save="needSave"
        :need-reset="needReset"
        :source-task="sourceTask"
      />
    </template>
  </EditDialog>
</template>
<script>
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
      preflightReady: false,
    };
  },
  watch: {
    async dialog(val) {
      this.$emit('input', val);
    },

    async value(val) {
      this.dialog = val;
      if (val) this.preflightReady = false;
    },
  },

  computed: {
    saveButtonText() {
      return this.preflightReady
        ? this.$t('confirmExecution')
        : this.$t(this.TEMPLATE_TYPE_ACTION_TITLES[this.template?.type || '']);
    },
    templateTitle() {
      let res = this.template?.name || '';
      if (res.length > 16) {
        res = `${res.substring(0, 14)}...`;
      }

      return res;
    },
  },

  methods: {
    handlePreflight(clearSaveRequest) {
      this.preflightReady = true;
      clearSaveRequest();
    },
    closeDialog(e) {
      this.dialog = false;
      this.preflightReady = false;
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
}
</style>
