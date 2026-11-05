<template>
  <div v-if="item == null" data-testid="workflow-page" data-state="loading">
    <v-progress-linear
      indeterminate
      color="primary-darken-2"
      data-testid="workflow-loading"
    ></v-progress-linear>
  </div>
  <div v-else data-testid="workflow-page" data-state="ready">
    <YesNoDialog
      :title="$t('deleteWorkflow')"
      :text="$t('askDeleteWorkflow')"
      v-model="deleteDialog"
      @yes="remove()"
      test-id="workflowDeleteDialog"
    />
    <WorkflowRunDialog
      ref="workflowRunDialog"
      v-model="runDialog"
      :workflow="item"
      :project-id="projectId"
      :loading="starting"
      @start="runWorkflow"
    />
    <WorkflowTriggersDialog
      v-if="triggersAvailable"
      v-model="triggerDialog"
      :project-id="projectId"
      :workflow="item"
      :system-info="systemInfo"
    />

    <v-toolbar flat>
      <v-app-bar-nav-icon
        @click="showDrawer()"
        data-testid="workflow-drawerToggle"
      ></v-app-bar-nav-icon>
      <v-toolbar-title class="breadcrumbs flex-0-1" data-testid="workflow-title">
        <router-link
          class="breadcrumbs__item breadcrumbs__item--link"
          :to="`/project/${projectId}/workflows/`"
          data-testid="workflow-backToList"
        >
          {{ $t('workflows') }}
        </router-link>
        <v-icon>mdi-chevron-right</v-icon>
        <span class="breadcrumbs__item" data-testid="workflow-name">{{ item.name }}</span>
      </v-toolbar-title>

      <v-spacer></v-spacer>

      <v-btn
        v-if="triggersAvailable && canAdminister"
        icon
        :title="$t('workflowTriggers')"
        data-testid="workflow-triggers-open"
        @click="triggerDialog = true"
      >
        <v-icon>mdi-lightning-bolt</v-icon>
      </v-btn>

      <v-btn
        v-if="canRun"
        color="primary"
        variant="flat"
        class="mr-3"
        @click="runWorkflow()"
        data-testid="workflow-run"
      >
        {{ $t('run') }}
      </v-btn>

      <v-btn
        icon
        variant="text"
        color="error"
        @click="deleteDialog = true"
        v-if="canAdminister"
        data-testid="workflow-delete"
      >
        <v-icon>mdi-delete</v-icon>
      </v-btn>

      <v-btn
        icon
        variant="text"
        :to="`/project/${projectId}/workflows/${itemId}/edit`"
        v-if="canUpdate"
        data-testid="workflow-edit"
      >
        <v-icon>mdi-pencil</v-icon>
      </v-btn>
    </v-toolbar>

    <v-tabs data-testid="workflow-tabs">
      <v-tab
        :to="`/project/${projectId}/workflows/${itemId}/runs`"
        data-testid="workflow-tab-runs"
      >
        {{ $t('workflowRuns') }}
      </v-tab>
      <v-tab
        :to="`/project/${projectId}/workflows/${itemId}/stats`"
        data-testid="workflow-tab-stats"
      >
        {{ $t('workflowStats') }}
      </v-tab>
      <v-tab
        v-if="canAdminister"
        :to="`/project/${projectId}/workflows/${itemId}/perms`"
        data-testid="workflow-tab-perms"
      >
        {{ $t('Permissions') }}
      </v-tab>
    </v-tabs>

    <v-divider style="margin-top: -1px;" />

    <router-view
      :project-id="projectId"
      :workflow="item"
      :features="features"
      :is-admin="isAdmin"
    ></router-view>
  </div>
</template>

<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/workflow-view';

import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import YesNoDialog from '@/components/YesNoDialog.vue';
import ProjectMixin from '@/components/ProjectMixin';
import WorkflowRunDialog from '@/components/WorkflowRunDialog.vue';
import WorkflowTriggersDialog from '@/components/WorkflowTriggersDialog.vue';

const PREFLIGHT_FINGERPRINT_HEADER = 'X-Semaphore-Preflight-Fingerprint';
const PREFLIGHT_REVIEW_HEADER = ['X-Semaphore-Preflight', 'Token'].join('-');

export default {
  components: {
    YesNoDialog,
    WorkflowRunDialog,
    WorkflowTriggersDialog,
  },

  mixins: [ProjectMixin],

  props: {
    projectId: Number,
    systemInfo: Object,
    features: Object,
  },

  data() {
    return {
      item: null,
      deleteDialog: null,
      runDialog: false,
      starting: false,
      triggerDialog: false,
    };
  },

  computed: {
    ...enhancedComputed,
    canRun() {
      return Boolean(this.item?.effective_access?.start);
    },

    canUpdate() {
      return Boolean(this.item?.effective_access?.edit);
    },

    itemId() {
      return parseInt(this.$route.params.workflowId, 10);
    },
  },

  watch: {
    async itemId() {
      await this.loadData();
    },
  },

  async created() {
    await this.loadData();
  },

  methods: {
    ...enhancedMethods,
    showDrawer() {
      EventBus.$emit('i-show-drawer');
    },

    async runWorkflow(request) {
      if (request === undefined) {
        this.runDialog = true;
        return;
      }
      const payload = request?.payload === undefined ? request : request.payload;
      const review = request?.payload === undefined ? null : request.review;
      this.starting = true;
      try {
        const run = (await axios({
          method: 'post',
          url: `/api/project/${this.projectId}/workflows/${this.itemId}/runs`,
          data: payload || {},
          headers: review ? {
            [PREFLIGHT_FINGERPRINT_HEADER]: review.fingerprint,
            [PREFLIGHT_REVIEW_HEADER]: review.reviewToken,
          } : {},
          responseType: 'json',
        })).data;

        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: this.$t('workflowRunStarted'),
        });

        this.runDialog = false;
        await this.$router.push(
          `/project/${this.projectId}/workflows/${this.itemId}/runs/${run.id}`,
        );
      } catch (err) {
        if (this.$refs.workflowRunDialog?.isDeploymentWindowBlock(err)) {
          this.$refs.workflowRunDialog.adoptDeploymentWindowBlock(err.response.data);
          return;
        }
        if (err?.response?.status === 403
          && err?.response?.data?.error === 'DEPLOYMENT_WINDOW_OVERRIDE_FORBIDDEN'
          && this.$refs.workflowRunDialog) {
          this.$refs.workflowRunDialog.setDeploymentWindowOverrideError();
          return;
        }
        const fresh = err?.response?.data?.preflight;
        if (err?.response?.status === 409 && fresh && this.$refs.workflowRunDialog) {
          this.$refs.workflowRunDialog.adoptExecutionPreflight(fresh, payload);
          return;
        }
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
      } finally {
        this.starting = false;
      }
    },

    async remove() {
      try {
        await axios({
          method: 'delete',
          url: `/api/project/${this.projectId}/workflows/${this.itemId}`,
          responseType: 'json',
        });

        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: `Workflow "${this.item.name}" deleted`,
        });

        await this.$router.push({
          path: `/project/${this.projectId}/workflows`,
        });
      } catch (err) {
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
      } finally {
        this.deleteDialog = false;
      }
    },

    async loadData() {
      try {
        this.item = await this.loadProjectResource('workflows', this.itemId);
      } catch (err) {
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
      }
    },
  },
};
</script>
