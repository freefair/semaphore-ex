<template>
  <div v-if="item == null">
    <v-progress-linear indeterminate color="primary darken-2"></v-progress-linear>
  </div>
  <div v-else>
    <YesNoDialog
      :title="$t('deleteWorkflow')"
      :text="$t('askDeleteWorkflow')"
      v-model="deleteDialog"
      @yes="remove()"
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
      <v-app-bar-nav-icon @click="showDrawer()"></v-app-bar-nav-icon>
      <v-toolbar-title class="breadcrumbs">
        <router-link
          class="breadcrumbs__item breadcrumbs__item--link"
          :to="`/project/${projectId}/workflows/`"
        >
          {{ $t('workflows') }}
        </router-link>
        <v-icon>mdi-chevron-right</v-icon>
        <span class="breadcrumbs__item">{{ item.name }}</span>
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
        depressed
        class="mr-3"
        @click="runWorkflow()"
        data-testid="workflow-run"
      >
        {{ $t('run') }}
      </v-btn>

      <v-btn icon color="error" @click="deleteDialog = true" v-if="canAdminister">
        <v-icon>mdi-delete</v-icon>
      </v-btn>

      <v-btn
        icon
        :to="`/project/${projectId}/workflows/${itemId}/edit`"
        v-if="canUpdate"
      >
        <v-icon>mdi-pencil</v-icon>
      </v-btn>
    </v-toolbar>

    <v-tabs>
      <v-tab :to="`/project/${projectId}/workflows/${itemId}/runs`">
        {{ $t('workflowRuns') }}
      </v-tab>
      <v-tab :to="`/project/${projectId}/workflows/${itemId}/stats`">
        {{ $t('workflowStats') }}
      </v-tab>
    </v-tabs>

    <v-divider style="margin-top: -1px;" />

    <router-view
      :project-id="projectId"
      :workflow="item"
    ></router-view>
  </div>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import YesNoDialog from '@/components/YesNoDialog.vue';
import ProjectMixin from '@/components/ProjectMixin';
import WorkflowRunDialog from '@/components/WorkflowRunDialog.vue';
import WorkflowTriggersDialog from '@/components/WorkflowTriggersDialog.vue';
import { findCapabilityDecision } from '@/lib/capabilities';

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
    canRun() {
      return Boolean(this.item?.effective_access?.start);
    },

    canUpdate() {
      return Boolean(this.item?.effective_access?.edit);
    },

    canAdminister() {
      return Boolean(this.item?.effective_access?.administer);
    },

    triggerDecision() {
      return findCapabilityDecision(this.systemInfo, 'workflow_triggers');
    },

    triggersAvailable() {
      return Boolean(this.triggerDecision?.access?.includes('read'));
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
    showDrawer() {
      EventBus.$emit('i-show-drawer');
    },

    hasRunInputs(workflow) {
      return (workflow.parameters || []).length > 0
        || (workflow.nodes || []).some((node) => {
          const policy = node.override_policy || {};
          return (policy.inventory_ids || []).length
            || (policy.environment_ids || []).length
            || policy.allow_arguments
            || policy.allow_branch;
        });
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
          url: `/api/project/${this.projectId}/workflows/${this.itemId}/run`,
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
