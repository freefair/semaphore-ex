<template>
  <EditDialog
    v-model="dialog"
    :save-button-text="itemId === 'new' ? $t('create') : $t('save')"
    :title="title"
    @save="onSave"
  >
    <template v-slot:form="{ onSave, onError, needSave, needReset }">
      <component
        :is="scope === 'workflow' ? 'WorkflowPermissionForm' : 'EditTemplatePermissionForm'"
        :template-id="objectId"
        :project-id="projectId"
        :object-id="objectId"
        :item-id="itemId"
        :scope="scope"
        @save="onSave"
        @error="onError"
        :need-save="needSave"
        :need-reset="needReset"
      />
    </template>
  </EditDialog>
</template>

<script>
import EditDialog from './EditDialog.vue';
import EditTemplatePermissionForm from './EditTemplatePermissionForm.vue';
import WorkflowPermissionForm from './enhanced/WorkflowPermissionForm.vue';

export default {
  components: {
    EditDialog,
    EditTemplatePermissionForm,
    WorkflowPermissionForm,
  },

  props: {
    value: Boolean,
    projectId: Number,
    scope: String,
    objectId: [String, Number],
    itemId: [String, Number],
  },

  data() {
    return {
      dialog: false,
    };
  },

  computed: {
    title() {
      const key = this.scope === 'workflow' ? 'workflowPermission' : 'templatePermission';
      return this.$t(key, { expr: this.itemId === 'new' ? this.$t('nnew') : this.$t('edit') });
    },
  },

  watch: {
    async dialog(val) {
      this.$emit('input', val);
    },

    async value(val) {
      this.dialog = val;
    },
  },

  methods: {
    onSave(e) {
      this.$emit('save', e);
    },
  },
};
</script>
