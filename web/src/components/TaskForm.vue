<template>
  <v-form
    ref="form"
    lazy-validation
    v-model="formValid"
    v-if="isLoaded()"
    @submit.prevent="save()"
  >
    <v-alert
      :value="formError"
      color="error"
      class="pb-2"
    >{{ formError }}
    </v-alert>

    <v-alert
      color="blue"
      dark
      dismissible
      dense
      @input="item.commit_hash=null"
      v-model="hasCommit"
      class="overflow-hidden mt-2"
    >
      <div
        style="font-weight: bold;"
      >
        <v-icon small>mdi-source-fork</v-icon>
        {{ (item.commit_hash || '').substr(0, 10) }}
      </div>
      <div v-if="sourceTask && sourceTask.commit_message">
        {{ sourceTask.commit_message.substring(0, 50) }}
      </div>
    </v-alert>

    <v-autocomplete
      v-if="buildTasks != null && template.type === 'deploy'"
      v-model="item.build_task_id"
      :label="$t('buildVersion')"
      :items="buildTasks"
      item-value="id"
      :item-text="(itm) => getTaskMessage(itm)"
      :rules="[v => !!v || $t('build_version_required')]"
      required
      :disabled="formSaving"
      outlined
      dense
    />

    <v-skeleton-loader
      v-else-if="template.type === 'deploy'"
      type="card"
      height="54"
      style="margin-bottom: 16px; margin-top: 4px;"
    ></v-skeleton-loader>

    <v-text-field
      v-model="item.message"
      :label="$t('messageOptional')"
      :disabled="formSaving"
      outlined
      dense
    />

    <div v-for="(v) in template.survey_vars || []" :key="v.name">

      <v-text-field
        v-if="v.type === 'secret'"
        :label="v.title"
        :hint="v.description"
        v-model="editedSecretEnvironment[v.name]"
        :required="v.required"
        class="masked-secret-input"
        :rules="[
            val => !v.required || !!val || v.title + $t('isRequired'),
          ]"
        outlined
        dense
      />

      <v-select
        clearable
        v-else-if="v.type === 'enum' || v.type === 'select'"
        :label="v.title + (v.required ? ' *' : '')"
        :hint="v.description"
        v-model="editedEnvironment[v.name]"
        :required="v.required"
        :rules="[
          val => !v.required || (Array.isArray(val) ? val.length > 0 : val != null)
          || v.title + ' ' + $t('isRequired')
        ]"
        :items="v.values"
        item-text="name"
        item-value="value"
        outlined
        dense
        :multiple="v.type === 'select'"
        :chips="v.type === 'select'"
      >
      <template v-if="v.type === 'select'" v-slot:selection="{ item, index }">
        <v-chip
          small
          close
          @click:close="deleteItem(v.name, index)"
        >
          {{ item && item.name ? item.name : String(item) }}
        </v-chip>
      </template>
    </v-select>

      <v-textarea
        v-else-if="v.type === 'text'"
        :label="v.title + (v.required ? ' *' : '')"
        :hint="v.description"
        v-model="editedEnvironment[v.name]"
        :required="v.required"
        :rules="[
          val => !v.required || !!val || v.title + ' ' + $t('isRequired'),
        ]"
        rows="3"
        auto-grow
        outlined
        dense
      />

      <v-text-field
        v-else
        :label="v.title + (v.required ? ' *' : '')"
        :hint="v.description"
        v-model="editedEnvironment[v.name]"
        :required="v.required"
        :rules="[
          val => !v.required || !!val || v.title + ' ' + $t('isRequired'),
          val => !val || v.type !== 'int' || /^\d+$/.test(val) ||
          v.title + ' ' + $t('mustBeInteger'),
        ]"
        outlined
        dense
      />
    </div>

    <v-text-field
      v-model="git_branch"
      :label="fieldLabel('branch')"
      outlined
      dense
      required
      :disabled="formSaving"
      v-if="
        needField('allow_override_branch')
        && template.allow_override_branch_in_task"
    />

    <v-autocomplete
      v-model="inventory_id"
      :label="fieldLabel('inventory')"
      :items="inventory"
      item-value="id"
      item-text="name"
      outlined
      dense
      required
      :disabled="formSaving"
      v-if="inventory != null && needInventory"
    ></v-autocomplete>

    <v-skeleton-loader
      v-else-if="needInventory"
      type="card"
      height="46"
      style="margin-bottom: 16px; margin-top: 4px;"
    ></v-skeleton-loader>

    <TaskParamsAnsibleForm
      v-if="template.app === 'ansible'"
      v-model="item.params"
      :app="template.app"
      :template-params="template.task_params || {}"
    />

    <TaskParamsTerraformForm
      v-else-if="['terraform', 'tofu', 'terragrunt'].includes(template.app)"
      v-model="item.params"
      :app="template.app"
      :template-params="template.task_params || {}"
    />

    <ArgsPicker
      v-if="template.allow_override_args_in_task"
      :vars="args"
      title="CLI args"
      @change="setArgs"
    />

    <v-alert
      v-if="deploymentWindowBlock"
      type="warning"
      text
      dense
      data-testid="deployment-window-blocked"
    >
      <div>{{ deploymentWindowBlockMessage }}</div>
      <div
        v-if="deploymentWindowBlock.next_eligible_known
          && deploymentWindowBlock.next_eligible_at"
        class="text-caption mt-1"
      >
        {{ $t('deploymentWindowNextEligible') }}:
        {{ formatDeploymentWindowDate(deploymentWindowBlock.next_eligible_at) }}
      </div>
    </v-alert>

    <template v-if="deploymentWindowBlock">
      <div class="text-subtitle-2 mb-2">{{ $t('deploymentWindowEmergencyOverride') }}</div>
      <v-select
        v-model="deploymentWindowOverrideCategory"
        :items="deploymentWindowOverrideCategories"
        :label="$t('deploymentWindowOverrideCategory')"
        outlined
        dense
        :disabled="formSaving"
      />
      <v-text-field
        v-model.trim="deploymentWindowOverrideReference"
        :label="$t('deploymentWindowOverrideReference')"
        :hint="$t('deploymentWindowOverrideReferenceHint')"
        persistent-hint
        outlined
        dense
        :disabled="formSaving"
        data-testid="deployment-window-override-reference"
      />
      <v-checkbox
        v-model="deploymentWindowOverrideConfirmed"
        :label="$t('deploymentWindowOverrideConfirm')"
        :disabled="formSaving"
        data-testid="deployment-window-override-confirm"
      />
    </template>

    <ExecutionPreflightReview :plan="executionPreflight" />

  </v-form>
</template>
<script>
/* eslint-disable import/no-extraneous-dependencies,import/extensions */

import ItemFormBase from '@/components/ItemFormBase';
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
import ArgsPicker from '@/components/ArgsPicker.vue';
import AppFieldsMixin from '@/components/AppFieldsMixin';
import TaskParamsAnsibleForm from '@/components/TaskParamsAnsibleForm.vue';
import TaskParamsTerraformForm from '@/components/TaskParamsTerraformForm.vue';
import ExecutionPreflightReview from '@/components/ExecutionPreflightReview.vue';

const PREFLIGHT_FINGERPRINT_HEADER = 'X-Semaphore-Preflight-Fingerprint';
const PREFLIGHT_REVIEW_HEADER = ['X-Semaphore-Preflight', 'Token'].join('-');

export default {
  mixins: [ItemFormBase, AppFieldsMixin],

  props: {
    template: Object,
    sourceTask: Object,
  },

  components: {
    TaskParamsAnsibleForm,
    TaskParamsTerraformForm,
    ArgsPicker,
    ExecutionPreflightReview,
  },

  data() {
    return {
      buildTasks: null,
      hasCommit: null,
      editedEnvironment: null,
      editedSecretEnvironment: null,
      cmOptions: {
        tabSize: 2,
        mode: 'application/json',
        lineNumbers: true,
        line: true,
        lint: true,
        indentWithTabs: false,
      },
      inventory: null,
      executionPreflight: null,
      executionPreflightPayloadSignature: null,
      deploymentWindowBlock: null,
      deploymentWindowOverrideCategory: null,
      deploymentWindowOverrideReference: '',
      deploymentWindowOverrideConfirmed: false,
    };
  },

  computed: {
    needInventory() {
      return this.needField('inventory') && this.template.task_params?.allow_override_inventory;
    },

    args() {
      let res = this.item.arguments;

      if (res == null) {
        res = this.template.arguments;
      }

      if (res == null) {
        res = '[]';
      }

      return JSON.parse(res);
    },

    app() {
      return this.template.app;
    },

    deploymentWindowOverrideCategories() {
      return [
        { text: this.$t('deploymentWindowOverrideIncident'), value: 'incident' },
        { text: this.$t('deploymentWindowOverrideSecurity'), value: 'security' },
        { text: this.$t('deploymentWindowOverrideCustomerImpact'), value: 'customer_impact' },
      ];
    },

    deploymentWindowOverrideReady() {
      return Boolean(this.deploymentWindowBlock
        && this.deploymentWindowOverrideConfirmed
        && ['incident', 'security', 'customer_impact']
          .includes(this.deploymentWindowOverrideCategory)
        && /^[A-Z0-9]{2,16}-[1-9][0-9]{0,9}$/
          .test(this.deploymentWindowOverrideReference));
    },

    deploymentWindowBlockMessage() {
      return this.$t(`deploymentWindowBlocked_${this.deploymentWindowBlock?.reason || 'unknown'}`);
    },

    inventory_id: {
      get() {
        return (this.item || {}).inventory_id || this.template.inventory_id;
      },
      set(newValue) {
        this.item.inventory_id = newValue;
      },
    },

    git_branch: {
      get() {
        return (this.item || {}).git_branch || this.template.git_branch;
      },
      set(newValue) {
        this.item.git_branch = newValue;
      },
    },
  },

  watch: {
    needReset(val) {
      if (val) {
        if (this.item) {
          this.item.template_id = this.template.id;
        }
        this.buildTasks = null;
        this.inventory = null;
        // this.template = null;
      }
    },

    template(val) {
      if (this.item) {
        this.item.template_id = val?.id;
      }
    },

    sourceTask(val) {
      this.assignItem(val);
    },

    hasCommit(val) {
      if (val == null) {
        this.commit_hash = null;
      }
    },
  },

  created() {
    this.refreshItem();
  },

  methods: {

    setArgs(args) {
      this.item.arguments = JSON.stringify(args || []);
    },

    deleteItem(name, index) {
      if (Array.isArray(this.editedEnvironment?.[name])) {
        this.editedEnvironment[name].splice(index, 1);
      }
    },

    getTaskMessage(task) {
      let buildTask = task;

      while (buildTask.version == null && buildTask.build_task != null) {
        buildTask = buildTask.build_task;
      }

      if (!buildTask) {
        return '';
      }

      return buildTask.version + (buildTask.message ? ` — ${buildTask.message}` : '');
    },

    assignItem(val) {
      const v = val || {};

      if (this.item == null) {
        this.item = {};
      }

      Object.keys(v).forEach((field) => {
        this.item[field] = v[field];
      });

      this.editedEnvironment = JSON.parse(v.environment || '{}');
      this.editedSecretEnvironment = JSON.parse(v.secret || '{}');
      this.hasCommit = v.commit_hash != null;
      this.executionPreflight = null;
      this.executionPreflightPayloadSignature = null;
      this.clearDeploymentWindowBlock();

      this.normalizeSelectValues();
    },

    isLoaded() {
      return this.item != null && this.template != null;
    },

    beforeSave() {
      this.item.environment = JSON.stringify(this.editedEnvironment);
      this.item.secret = JSON.stringify(this.editedSecretEnvironment);
    },

    taskSavePayload() {
      return {
        ...this.item,
        project_id: this.projectId,
      };
    },

    taskStartPayload(payload) {
      if (!this.deploymentWindowOverrideReady) return payload;
      return {
        ...payload,
        deployment_window_override: {
          category: this.deploymentWindowOverrideCategory,
          reference: this.deploymentWindowOverrideReference,
        },
      };
    },

    isDeploymentWindowBlock(err) {
      return err?.response?.status === 409
        && err?.response?.data?.state === 'blocked';
    },

    adoptDeploymentWindowBlock(decision) {
      this.deploymentWindowBlock = decision;
      this.deploymentWindowOverrideCategory = null;
      this.deploymentWindowOverrideReference = '';
      this.deploymentWindowOverrideConfirmed = false;
      this.formError = null;
    },

    clearDeploymentWindowBlock() {
      this.deploymentWindowBlock = null;
      this.deploymentWindowOverrideCategory = null;
      this.deploymentWindowOverrideReference = '';
      this.deploymentWindowOverrideConfirmed = false;
    },

    formatDeploymentWindowDate(value) {
      return new Date(value).toLocaleString();
    },

    isExecutionPreflightUnavailable(err) {
      const response = err?.response;
      return response?.status === 404
        && response?.data?.error === 'CAPABILITY_DENIED'
        && response?.data?.capability === 'execution_preflight';
    },

    async submitTaskPayload(payload, headers = {}) {
      const item = (await axios({
        method: 'post',
        url: this.getItemsUrl(),
        responseType: 'json',
        data: payload,
        headers,
      })).data;
      await this.afterSave(item);
      this.$emit('save', { item: item || this.item, action: this.getSaveAction() });
      return item || this.item;
    },

    async save() {
      this.formError = null;
      if (!this.$refs.form.validate()) {
        this.$emit('error', {});
        return null;
      }
      if (this.deploymentWindowBlock && !this.deploymentWindowOverrideReady) {
        this.formError = this.$t('deploymentWindowOverrideIncomplete');
        this.$emit('error', { message: this.formError });
        return null;
      }
      this.formSaving = true;
      try {
        await this.beforeSave();
        const payload = this.taskSavePayload();
        const signature = JSON.stringify(payload);
        if (!this.executionPreflight || signature !== this.executionPreflightPayloadSignature) {
          try {
            this.executionPreflight = (await axios.post(`/api/project/${this.projectId}/tasks/preflight`, payload)).data;
          } catch (err) {
            if (this.isExecutionPreflightUnavailable(err)) {
              return await this.submitTaskPayload(this.taskStartPayload(payload));
            }
            throw err;
          }
          this.executionPreflightPayloadSignature = signature;
          this.$emit('preflight', this.executionPreflight);
          if ((this.executionPreflight.findings || []).some(({ severity }) => severity === 'denial')) {
            this.formError = this.$t('executionPreflightDenied');
          }
          return null;
        }
        if ((this.executionPreflight.findings || []).some(({ severity }) => severity === 'denial')) {
          this.formError = this.$t('executionPreflightDenied');
          this.$emit('preflight', this.executionPreflight);
          return null;
        }
        return await this.submitTaskPayload(this.taskStartPayload(payload), {
          [PREFLIGHT_FINGERPRINT_HEADER]: this.executionPreflight.fingerprint,
          [PREFLIGHT_REVIEW_HEADER]: this.executionPreflight.review_token,
        });
      } catch (err) {
        if (this.isDeploymentWindowBlock(err)) {
          this.adoptDeploymentWindowBlock(err.response.data);
          this.$emit('error', {});
          return null;
        }
        if (err?.response?.status === 403
          && err?.response?.data?.error === 'DEPLOYMENT_WINDOW_OVERRIDE_FORBIDDEN') {
          this.formError = this.$t('deploymentWindowOverrideForbidden');
          this.$emit('error', { message: this.formError });
          return null;
        }
        const fresh = err?.response?.data?.preflight;
        if (err?.response?.status === 409 && fresh) {
          this.executionPreflight = fresh;
          this.executionPreflightPayloadSignature = JSON.stringify(this.taskSavePayload());
          this.formError = this.$t('executionPreflightChanged');
          this.$emit('preflight', fresh);
          return null;
        }
        this.formError = getErrorMessage(err);
        this.$emit('error', { message: this.formError });
        return null;
      } finally {
        this.formSaving = false;
      }
    },

    refreshItem() {
      this.assignItem(this.sourceTask);

      this.item.template_id = this.template.id;

      if (!this.item.params) {
        this.item.params = {};
      }

      ['tags', 'limit', 'skip_tags'].forEach((param) => {
        if (!this.item.params[param]) {
          this.item.params[param] = (this.template.task_params || {})[param];
        }
      });
    },

    async afterLoadData() {
      this.refreshItem();

      [
        this.buildTasks,
        this.inventory,
      ] = await Promise.all([

        this.template.type === 'deploy' ? (await axios({
          keys: 'get',
          url: `/api/project/${this.projectId}/templates/${this.template.build_template_id}/tasks?status=success&limit=20`,
          responseType: 'json',
        })).data.filter((task) => task.status === 'success') : [],

        this.needInventory ? (await axios({
          keys: 'get',
          url: this.getInventoryUrl(),
          responseType: 'json',
        })).data : [],
      ]);

      if (this.item.build_task_id == null
        && this.buildTasks.length > 0
        && this.buildTasks.length > 0) {
        this.item.build_task_id = this.buildTasks[0].id;
      }

      ['tags', 'limit', 'skip_tags'].forEach((param) => {
        if (!this.item.params[param]) {
          this.item.params[param] = (this.template.task_params || {})[param];
        }
      });

      const defaultVars = (this.template.survey_vars || [])
        .filter((s) => s.default_value)
        .reduce((res, curr) => ({
          ...res,
          [curr.name]: curr.default_value,
        }), {});

      this.editedEnvironment = {
        ...defaultVars,
        ...this.editedEnvironment,
      };

      this.normalizeSelectValues();
    },

    getInventoryUrl() {
      let res = `/api/project/${this.projectId}/inventory?app=${this.app}`;
      switch (this.app) {
        case 'terraform':
        case 'tofu':
          res += `&template_id=${this.template.id}`;
          break;
        default:
          break;
      }
      return res;
    },

    getItemsUrl() {
      return `/api/project/${this.projectId}/tasks`;
    },

    normalizeSelectValues() {
      if (!this.template || !this.template.survey_vars) return;
      this.template.survey_vars.forEach((sv) => {
        if (sv.type !== 'select') return;
        const cur = this.editedEnvironment[sv.name];
        if (cur == null || cur === '') {
          this.$set(this.editedEnvironment, sv.name, []);
        } else if (!Array.isArray(cur)) {
          this.$set(this.editedEnvironment, sv.name, [cur]);
        }
      });
    },
  },
};
</script>
