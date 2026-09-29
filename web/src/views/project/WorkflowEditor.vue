<template>
  <div class="WorkflowEditor">
    <WorkflowVersionsDialog
      v-if="!isNew"
      v-model="versionsDialog"
      :project-id="projectId"
      :workflow-id="workflowId"
      :can-restore="canManage"
      @restored="onVersionRestored"
    />
    <CrossProjectTemplateGrantsDialog
      v-if="canManageCrossProjectTemplates"
      v-model="crossProjectGrantsDialog"
      :project-id="projectId"
      @changed="refreshCrossProjectReferences"
    />
    <YesNoDialog
      v-model="leaveDialog"
      :title="$t('workflowUnsavedChanges')"
      :text="$t('workflowUnsavedLeave')"
      :yes-button-title="$t('workflowLeaveWithoutSaving')"
      @yes="confirmLeave()"
      @no="cancelLeave()"
    />

    <v-toolbar flat class="WorkflowEditor__toolbar">
      <v-app-bar-nav-icon @click="showDrawer()"></v-app-bar-nav-icon>
      <v-toolbar-title class="WorkflowEditor__title d-flex align-center">
        <router-link :to="`/project/${projectId}/workflows`">
          {{ $t('workflows') }}
        </router-link>
        <span class="mx-2">/</span>
        <span
          v-if="item != null"
          class="WorkflowEditor__nameWrap"
          :class="{ 'WorkflowEditor__nameWrap--disabled': !canManage }"
        >
          <v-icon small class="WorkflowEditor__nameIcon">mdi-pencil</v-icon>
          <span class="WorkflowEditor__nameSizer" :data-value="item.name || $t('newWorkflow')">
            <input
              v-model="item.name"
              :placeholder="$t('newWorkflow')"
              :disabled="!canManage"
              :aria-label="$t('name')"
              size="1"
              class="WorkflowEditor__nameField"
              @input="markDirty"
            />
          </span>
        </span>
        <v-chip
          v-if="item != null && item.revision"
          x-small
          outlined
          class="ml-3"
          :title="$t('workflowRevisionHint')"
        >{{ $t('workflowRevisionLabel', { number: item.revision }) }}</v-chip>
        <v-tooltip v-if="activeRuns > 0" bottom>
          <template v-slot:activator="{ on, attrs }">
            <v-chip x-small outlined color="warning" class="ml-2" v-bind="attrs" v-on="on">
              <v-icon x-small left>mdi-play-circle-outline</v-icon>
              {{ $tc('workflowActiveRunsCount', activeRuns, { count: activeRuns }) }}
            </v-chip>
          </template>
          <span>{{ $t('workflowActiveRunsHint') }}</span>
        </v-tooltip>
      </v-toolbar-title>

      <v-spacer></v-spacer>

      <v-btn
        v-if="!isNew"
        icon
        :title="$t('workflowVersionHistory')"
        data-testid="workflow-version-history"
        @click="versionsDialog = true"
      >
        <v-icon>mdi-history</v-icon>
      </v-btn>

      <v-btn
        v-if="canManageCrossProjectTemplates"
        icon
        :title="$t('crossProjectTemplates')"
        data-testid="workflow-cross-project-template-grants"
        @click="crossProjectGrantsDialog = true"
      >
        <v-icon>mdi-share-variant-outline</v-icon>
      </v-btn>

      <v-btn icon :disabled="!canUndo" :title="$t('workflowUndo')" @click="undo()">
        <v-icon>mdi-undo</v-icon>
      </v-btn>
      <v-btn icon :disabled="!canRedo" :title="$t('workflowRedo')" class="mr-2" @click="redo()">
        <v-icon>mdi-redo</v-icon>
      </v-btn>

      <v-btn
        text
        :disabled="!canManage || validating || saving"
        :loading="validating"
        @click="validate()"
      >{{ $t('workflowValidate') }}
      </v-btn>

      <v-btn
        text
        :disabled="!canManage || !dirty || validating || saving"
        @click="discard()"
      >{{ $t('discard') }}
      </v-btn>

      <v-btn
        color="primary"
        :disabled="!canManage || saving || validating || clientIssues.length > 0
          || versionMessageTooLong"
        :loading="saving"
        @click="save()"
      >{{ $t('save') }}
      </v-btn
      >
    </v-toolbar>

    <v-divider />

    <v-alert
      v-if="conflict"
      type="warning"
      prominent
      tile
      class="mb-0"
    >
      <div class="d-flex align-center">
        <span>{{ $t('workflowConflictMessage') }}</span>
        <v-spacer />
        <v-btn color="warning" outlined @click="reloadAfterConflict()">
          {{ $t('workflowReloadServerVersion') }}
        </v-btn>
      </div>
    </v-alert>

    <div class="WorkflowEditor__body" v-if="item != null && templates != null">
      <!-- Palette + meta -->
      <div
        class="WorkflowEditor__side WorkflowEditor__side--left"
        :class="{ 'WorkflowEditor__side--collapsed': sideCollapsed }"
      >
        <button
          type="button"
          class="WorkflowEditor__sideToggle"
          :title="$t(sideCollapsed ? 'workflowSidebarExpand' : 'workflowSidebarCollapse')"
          @click="toggleSide()"
        >
          <v-icon small>
            {{ sideCollapsed ? 'mdi-chevron-right' : 'mdi-chevron-left' }}
          </v-icon>
        </button>

        <div class="WorkflowEditor__sideScroll">
          <template v-if="!sideCollapsed">
            <WorkflowDefinitionSettings
              :version-message.sync="item.version_message"
              :max-parallel-tasks.sync="item.max_parallel_tasks"
              :view-role-ids.sync="item.access_policy.view_role_ids"
              :start-role-ids.sync="item.access_policy.start_role_ids"
              :parameters.sync="item.parameters"
              :project-id="projectId"
              :workflow-role-options="workflowRoleOptions"
              :version-message-too-long="versionMessageTooLong"
              :can-manage="canManage"
              :can-administer="canAdminister"
              @dirty="markDirty"
            >
              <template #start-version>
                <v-text-field
                  v-model="item.start_version"
                  :label="$t('startVersion')"
                  :hint="$t('workflowStartVersionHint')"
                  persistent-hint
                  class="mb-4"
                  :disabled="!canManage"
                  outlined
                  dense
                  @input="markDirty"
                />
              </template>
            </WorkflowDefinitionSettings>

            <v-divider />
          </template>

          <div class="pa-3">
            <template v-if="!sideCollapsed">
              <div class="text-subtitle-2 mb-1">{{ $t('workflowEditorPalette') }}</div>
              <div class="text-caption text--secondary mb-3">
                {{ $t('workflowPaletteClickHint') }}
              </div>
            </template>
            <div
              v-for="p in palette"
              :key="p.kind"
              class="WorkflowEditor__paletteItem"
              :class="`WorkflowEditor__paletteItem--${p.kind}`"
              :draggable="canManage"
              :title="sideCollapsed ? p.text : null"
              @dragstart="onDragStart($event, p.kind)"
              @click="addFromPalette(p.kind)"
            >
              <span
                class="WorkflowEditor__paletteTile"
                :class="`WorkflowEditor__paletteTile--${p.kind}`"
              >
                <v-icon small :color="p.color">{{ p.icon }}</v-icon>
              </span>
              <span v-if="!sideCollapsed" class="WorkflowEditor__paletteText">{{ p.text }}</span>
            </div>
          </div>
          <div v-if="!sideCollapsed" class="pa-3" data-testid="workflow-validation-status">
            <div class="text-subtitle-2 mb-2">{{ $t('workflowProblemsPanelTitle') }}</div>
            <v-alert v-if="problems.length === 0" text dense
              :type="validationState === 'valid' ? 'success' : 'info'">
              {{ validationState === 'valid'
                ? $t('workflowValidationPassed') : $t('workflowValidationNotRun') }}
            </v-alert>
            <v-alert v-for="(problem, index) in problems" :key="index" text dense type="warning">
              {{ problemText(problem) }}
            </v-alert>
          </div>
          <v-btn v-else-if="problems.length" icon color="warning"
            data-testid="workflow-validation-collapsed"
            :title="problems.map(problemText).join('\n')"
            :aria-label="$t('workflowProblemsPanelTitle')"
            @click="sideCollapsed = false">
            <v-icon>mdi-alert</v-icon>
          </v-btn>
        </div>
      </div>

      <!-- Canvas -->
      <div class="WorkflowEditor__canvas">
        <WorkflowGraph
          ref="graph"
          :key="graphKey"
          :nodes="item.nodes"
          :edges="item.edges"
          :templates="workflowGraphTemplates"
          :quick-add-templates="templates || []"
          :editable="canManage"
          :node-problems="problemsByNode"
          @change="onGraphChange"
          @node-selected="onNodeSelected"
          @connection-selected="onConnectionSelected"
          @blocked="onBlocked"
        />
      </div>

      <!-- Properties panel -->
      <div
        v-if="editingNode || editingEdge"
        class="WorkflowEditor__side WorkflowEditor__side--right"
      >
        <div v-if="editingNode" class="pa-3">
          <div class="d-flex align-center mb-2">
            <span class="text-subtitle-2"> {{ $t('workflowNodeId') }} #{{ editingNode.id }} </span>
            <v-spacer />
            <v-btn icon small :disabled="!canManage" @click="deleteSelectedNode()">
              <v-icon small>mdi-delete</v-icon>
            </v-btn>
          </div>

          <v-text-field
            v-model="editingNode.display_name"
            :label="$t('workflowDisplayName')"
            :disabled="!canManage"
            outlined
            dense
            hide-details="auto"
            class="mb-5"
            @input="applyNodeEdit"
          />

          <v-textarea
            v-if="editingNode.kind === 'note'"
            v-model="editingNode.note"
            :label="$t('workflowNoteText')"
            :placeholder="$t('workflowNotePlaceholder')"
            :disabled="!canManage"
            outlined
            dense
            auto-grow
            rows="4"
            hide-details="auto"
            @input="applyNodeEdit"
          />

          <template v-if="editingNode.kind !== 'note'">
            <v-select
              v-model="editingNode.kind"
              :items="kindOptions"
              item-value="value"
              item-text="text"
              :label="$t('workflowNodeKind')"
              :disabled="!canManage"
              outlined
              dense
              hide-details="auto"
              class="mb-5"
              @change="onKindChanged"
            />

            <v-select
              v-model="editingNode.convergence_mode"
              :items="convergenceOptions"
              item-value="value"
              item-text="text"
              :label="$t('workflowConvergence')"
              :disabled="!canManage"
              outlined
              dense
              hide-details="auto"
              class="mb-5"
              @change="applyNodeEdit"
            />

            <v-select
              v-model="editingNode.join_mode"
              :items="joinOptions"
              item-value="value"
              item-text="text"
              :label="$t('workflowJoinMode')"
              :disabled="!canManage"
              outlined
              dense
              hide-details="auto"
              class="mb-5"
              @change="applyNodeEdit"
            />

            <v-autocomplete
              v-if="editingNode.kind !== 'approval' && editingNode.kind !== 'delay'"
              v-model="editingNodeTemplateChoice"
              :items="workflowTemplateChoices"
              item-value="value"
              item-text="text"
              :label="$t('taskTemplate')"
              :disabled="!canManage"
              :loading="crossProjectReferencesLoading"
              outlined
              dense
              hide-details="auto"
              class="mb-5"
            />

            <v-alert
              v-if="editingNodeCrossProjectReference"
              :type="editingNodeCrossProjectReference.available ? 'info' : 'warning'"
              text
              dense
              class="mb-5"
            >
              {{ editingNodeCrossProjectReference.available
                ? $t('crossProjectReferencePinned')
                : $t('crossProjectReferenceUnavailable') }}
            </v-alert>

            <v-card
            v-if="editingNode.kind !== 'approval'
              && editingNode.kind !== 'delay'
              && editingNodeTemplate"
              :key="`task-params-${editingNode.id}-${editingNode.template_id}`"
              style="background: rgba(133, 133, 133, 0.06)"
              class="mb-6 pt-3"
            >

              <div style="
                position: absolute;
                background: var(--highlighted-card-bg-color);
                width: 28px;
                height: 28px;
                transform: rotate(45deg);
                left: calc(50% - 14px);
                top: -14px;
                border-radius: 0;
              "></div>

              <v-card-text class="py-0">
                <TaskParamsForm
                  :template="editingNodeTemplate"
                  v-model="editingNode.task_params"
                  class="mt-2"
                  @input="applyNodeEdit"
                />
              </v-card-text>
            </v-card>

            <WorkflowNodeOverridePolicyEditor
              v-if="editingNode.kind === 'task' && editingNodeTemplate"
              v-model="editingNode.override_policy"
              :project-id="projectId"
              :template="editingNodeTemplate"
              :parameters="item.parameters"
              :disabled="!canManage"
              @input="applyNodeEdit"
            />

            <WorkflowNodeArtifactsEditor
              v-if="editingNode.kind === 'task'"
              :outputs="editingNode.artifact_outputs"
              :inputs="editingNode.artifact_inputs"
              :artifact-output-types="artifactOutputTypes"
              :reachable-artifact-outputs="reachableArtifactOutputs"
              :artifact-reference-key="artifactReferenceKey"
              :can-manage="canManage"
              @add-output="addArtifactOutput"
              @remove-output="removeArtifactOutput"
              @output-type="setArtifactOutputType"
              @output-field="updateArtifactOutputField"
              @add-input="addArtifactInput"
              @remove-input="removeArtifactInput"
              @input-reference="setArtifactReference"
              @input-field="updateArtifactInputField"
            />

            <WorkflowNodeApprovalPolicy
              v-if="editingNode.kind === 'approval'"
              :policy="editingNode.approval_role_policy"
              :timeout-outcome.sync="editingNode.approval_timeout_outcome"
              :workflow-role-options="workflowRoleOptions"
              :approval-role-mode-options="approvalRoleModeOptions"
              :approval-timeout-outcome-options="approvalTimeoutOutcomeOptions"
              :can-manage="canManage"
              :can-administer="canAdminister"
              @policy-field="updateApprovalPolicyField"
              @policy-change="onApprovalPolicyChanged"
              @edit="applyNodeEdit"
            >
              <v-text-field
                v-model.number="editingNode.approval_timeout"
                type="number"
                min="1"
                :label="$t('workflowApprovalTimeout')"
                :disabled="!canManage"
                outlined
                dense
                hide-details="auto"
                class="mb-2"
                @change="applyNodeEdit"
              />
              <v-text-field
                v-model="editingNode.approval_message"
                :label="$t('workflowApprovalMessage')"
                :disabled="!canManage"
                outlined
                dense
                hide-details="auto"
                @change="applyNodeEdit"
              />
            </WorkflowNodeApprovalPolicy>
            <template v-if="editingNode.kind === 'delay'">
              <v-text-field
                v-model.number="editingNode.delay_seconds"
                type="number"
                min="1"
                :label="$t('workflowDelaySeconds')"
                :hint="$t('workflowDelayHint')"
                persistent-hint
                :disabled="!canManage"
                outlined
                dense
                hide-details="auto"
                class="mb-2"
                @change="applyNodeEdit"
              />
            </template>
          </template>
        </div>

        <div v-else-if="editingEdge" class="pa-3">
          <div class="text-subtitle-2 mb-2">{{ $t('workflowEdgeCondition') }}</div>
          <div class="text-caption text--secondary mb-2">
            #{{ editingEdge.source_node_id }} → #{{ editingEdge.destination_node_id }}
          </div>
          <v-select
            v-model="editingEdge.condition"
            :items="conditionOptions"
            item-value="value"
            item-text="text"
            :disabled="!canManage"
            outlined
            dense
            hide-details="auto"
            @change="onEdgeConditionChanged"
          />
          <v-textarea
            v-if="editingEdge.condition === 'expression'"
            v-model="editingEdge.condition_expression"
            :label="$t('workflowConditionExpression')"
            :hint="$t('workflowConditionExpressionHint')"
            persistent-hint
            :disabled="!canManage"
            outlined
            dense
            auto-grow
            rows="2"
            class="mt-4"
            @input="applyEdgeEdit"
          />
          <v-text-field
            v-model="editingEdge.label"
            :label="$t('workflowEdgeLabel')"
            :disabled="!canManage"
            outlined
            dense
            hide-details="auto"
            class="mt-4"
            @input="applyEdgeEdit"
          />
        </div>
      </div>
    </div>

    <div v-else class="pa-4 text-center">
      <v-progress-circular indeterminate color="primary" />
    </div>
  </div>
</template>

<script>
import createEnhancedState from '@/lib/enhanced/workflow-editor-state';

import WorkflowDefinitionSettings from '@/components/enhanced/workflow/DefinitionSettings.vue';
import WorkflowNodeArtifactsEditor from '@/components/enhanced/workflow/NodeArtifactsEditor.vue';
import WorkflowNodeApprovalPolicy from '@/components/enhanced/workflow/NodeApprovalPolicy.vue';
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/workflow-editor';

import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import WorkflowGraph from '@/components/WorkflowGraph.vue';
import WorkflowNodeOverridePolicyEditor from '@/components/WorkflowNodeOverridePolicyEditor.vue';
import WorkflowVersionsDialog from '@/components/WorkflowVersionsDialog.vue';
import CrossProjectTemplateGrantsDialog from '@/components/CrossProjectTemplateGrantsDialog.vue';
import YesNoDialog from '@/components/YesNoDialog.vue';
import TaskParamsForm from '@/components/TaskParamsForm.vue';
import ProjectMixin from '@/components/ProjectMixin';
import PermissionsCheck from '@/components/PermissionsCheck';
import { USER_PERMISSIONS } from '@/lib/constants';
import { layoutWorkflowNodes, needsAutoLayout } from '@/lib/workflowLayout';
import { WORKFLOW_DEFINITION_VERSION } from '@/lib/workflowValidation';

import WorkflowHistory from '@/lib/workflowHistory';
import { readSideCollapsed, writeSideCollapsed } from '@/lib/workflowEditorPrefs';

function isTypingTarget(target) {
  if (!target) return false;
  const tag = (target.tagName || '').toLowerCase();
  return tag === 'input' || tag === 'textarea' || tag === 'select' || target.isContentEditable;
}

export default {
  components: {
    WorkflowDefinitionSettings,
    WorkflowNodeArtifactsEditor,
    WorkflowNodeApprovalPolicy,
    TaskParamsForm,
    WorkflowGraph,
    WorkflowNodeOverridePolicyEditor,
    WorkflowVersionsDialog,
    CrossProjectTemplateGrantsDialog,
    YesNoDialog,
  },
  mixins: [ProjectMixin, PermissionsCheck],
  props: {
    projectId: Number,
  },
  data() {
    return {
      item: null,
      baseline: null,
      templates: null,
      projectRoles: [],
      saving: false,
      ...createEnhancedState(),
      graphKey: 0,
      // Set before navigating new -> /edit after a create, so the route watcher
      // does not reload (which would reset the selection and rebuild the canvas).
      skipNextRouteReload: false,
      selectedNodeId: null,
      editingNode: null,
      editingEdge: null,
      // Collapses the palette and, through App.vue, the main navigation to
      // icon-only strips. Remembered per browser; App restores the navigation
      // when the editor is left (see beforeDestroy).
      sideCollapsed: readSideCollapsed(),
      canUndo: false,
      canRedo: false,
      // Runs still in progress: they keep executing the revision they started
      // from, so saving is safe — the chip in the toolbar just says so.
      activeRuns: 0,
      // JSON of the last loaded / saved model, for the unsaved-changes guard.
      savedSnapshot: null,
      leaveDialog: false,
      pendingLeave: null,
      USER_PERMISSIONS,
    };
  },
  computed: {
    ...enhancedComputed,
    workflowId() {
      const raw = this.$route.params.workflowId;
      return raw ? parseInt(raw, 10) : null;
    },
    isNew() {
      return this.workflowId == null;
    },
    canManage() {
      if (this.isNew) return this.can(USER_PERMISSIONS.editWorkflows);
      return this.item?.effective_access?.edit
        ?? this.can(USER_PERMISSIONS.editWorkflows);
    },
    editingNodeTemplate() {
      if (!this.editingNode || !this.editingNode.template_id) return null;
      if (this.editingNode.cross_project_template_reference) return null;
      return this.templates.find((t) => t.id === this.editingNode.template_id) || null;
    },
    kindOptions() {
      return ['task', 'approval', 'delay'].map((value) => ({
        value, text: this.$t(`workflowNodeKind${value[0].toUpperCase()}${value.slice(1)}`),
      }));
    },
    convergenceOptions() {
      return [
        { value: 'all', text: this.$t('workflowConvergenceAll') },
        { value: 'any', text: this.$t('workflowConvergenceAny') },
      ];
    },
    palette() {
      return [
        {
          kind: 'task', icon: 'mdi-cog', color: null, text: this.$t('workflowPaletteTaskNode'),
        },
        {
          kind: 'approval', icon: 'mdi-account-check', color: '#ab47bc', text: this.$t('workflowPaletteApprovalNode'),
        },
        {
          kind: 'delay', icon: 'mdi-timer-outline', color: '#ff9800', text: this.$t('workflowPaletteDelayNode'),
        },
        {
          kind: 'note', icon: 'mdi-note-text-outline', color: null, text: this.$t('workflowPaletteNoteNode'),
        },
      ];
    },
    modelSnapshot() {
      if (!this.item) return null;
      return JSON.stringify({
        name: this.item.name,
        start_version: this.item.start_version || '',
        nodes: this.item.nodes,
        edges: this.item.edges,
      });
    },
    conditionOptions() {
      return [
        { value: 'on_success', text: this.$t('workflowConditionOnSuccess') },
        { value: 'on_failure', text: this.$t('workflowConditionOnFailure') },
        { value: 'always', text: this.$t('workflowConditionAlways') },
        { value: 'expression', text: this.$t('workflowConditionExpression') },
      ];
    },
    problems() {
      if (this.validationIssues.length === 0) return this.clientIssues;
      const serverLocations = new Set(
        this.validationIssues.map((entry) => `${entry.code}:${entry.path || ''}`),
      );
      return [
        ...this.validationIssues,
        ...this.clientIssues.filter(
          (entry) => !serverLocations.has(`${entry.code}:${entry.path || ''}`),
        ),
      ];
    },
    problemsByNode() {
      const map = {};
      this.problems.forEach((p) => {
        const nodeId = p.nodeId ?? p.node_id;
        if (nodeId == null || map[nodeId]) return;
        map[nodeId] = this.problemText(p);
      });
      return map;
    },
  },
  watch: {
    baseline() {
      if (!this.history || !this.item) return;
      // Server-assigned IDs and restored versions start a new editing history.
      this.history.reset({ nodes: this.item.nodes, edges: this.item.edges });
      this.syncHistoryFlags();
    },
    sideCollapsed(val) {
      writeSideCollapsed(val);
      this.setNavMini(val);
    },
    '$route.params.workflowId': function reloadOnRoute() {
      if (this.skipNextRouteReload) {
        this.skipNextRouteReload = false;
        return;
      }
      this.loadData();
    },
  },
  beforeRouteLeave(to, from, next) {
    if (!this.dirty) {
      next();
      return;
    }
    this.pendingLeave = next;
    this.leaveDialog = true;
  },
  created() {
    this.history = new WorkflowHistory(50);
    this.pendingHistoryKey = null;
  },
  async mounted() {
    window.addEventListener('keydown', this.onWindowKeyDown);
    window.addEventListener('beforeunload', this.onBeforeUnload);
    // The editor fills the viewport exactly; drop the always-on page
    // scrollbar Vuetify puts on <html>, its empty track shows as a strip.
    document.documentElement.classList.add('WorkflowEditor-html');
    this.setNavMini(this.sideCollapsed);
    [this.templates, this.projectRoles] = await Promise.all([
      this.loadProjectResources('templates'),
      this.loadEndpoint(`/api/project/${this.projectId}/roles/all`),
    ]);
    await this.loadData();
    if (this.canManageCrossProjectTemplates) await this.refreshCrossProjectReferences();
  },
  beforeDestroy() {
    window.removeEventListener('keydown', this.onWindowKeyDown);
    window.removeEventListener('beforeunload', this.onBeforeUnload);
    document.documentElement.classList.remove('WorkflowEditor-html');
    // The collapsed navigation is an editor-only state; other pages get the
    // full drawer back regardless of what is stored.
    this.setNavMini(false);
  },
  methods: {
    ...enhancedMethods,
    showDrawer() {
      EventBus.$emit('i-show-drawer');
    },
    toggleSide() {
      this.sideCollapsed = !this.sideCollapsed;
    },
    setNavMini(mini) {
      EventBus.$emit('i-nav-mini', { mini });
    },
    // Informational only (see activeRuns); a failure must not block editing.
    async loadActiveRuns() {
      try {
        const runs = await this.loadEndpoint(
          `/api/project/${this.projectId}/workflows/${this.workflowId}/runs`,
        );
        this.activeRuns = (runs || [])
          .filter((r) => ['pending', 'queued', 'running', 'approval', 'stopping'].includes(r.status)).length;
      } catch (err) {
        this.activeRuns = 0;
      }
    },
    getNewItem() {
      return {
        name: '',
        description: '',
        definition_version: WORKFLOW_DEFINITION_VERSION,
        revision: 0,
        max_parallel_tasks: 4,
        version_message: '',
        access_policy: {
          revision: 0,
          view_role_ids: [],
          start_role_ids: [],
        },
        parameters: [],
        nodes: [],
        edges: [],
      };
    },
    async loadData() {
      this.resetEditorState();
      try {
        if (this.isNew) {
          this.item = this.prepareItem(this.getNewItem());
        } else {
          const loaded = await this.loadEndpoint(
            `/api/project/${this.projectId}/workflows/${this.workflowId}`,
          );
          this.item = this.prepareItem(loaded);
          this.autoLayout();
          this.loadActiveRuns();
        }
      } catch (err) {
        EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
        return;
      }
      this.baseline = this.clone(this.item);
      this.crossProjectReferences = this.crossProjectReferences.filter(
        (reference) => reference.available,
      );
      this.includePersistedCrossProjectReferences();
      this.dirty = false;
      this.history.reset({ nodes: this.item.nodes, edges: this.item.edges });
      this.syncHistoryFlags();
      this.savedSnapshot = this.modelSnapshot;
      // Force a clean canvas rebuild matching the freshly loaded model.
      this.graphKey += 1;
    },
    // Seed positions for legacy workflows whose nodes were created before the
    // graphical editor (all coordinates 0) so the computed layout persists on
    // the next save. Uses the same algorithm as the read-only run view.
    autoLayout() {
      const { nodes } = this.item;
      if (!needsAutoLayout(nodes)) return;
      const layout = layoutWorkflowNodes(nodes, this.item.edges);
      nodes.forEach((n, i) => {
        // Assign through the array (not the forEach param) to satisfy no-param-reassign.
        nodes[i].position_x = layout[n.id].x;
        nodes[i].position_y = layout[n.id].y;
      });
    },

    // ---- palette / canvas glue ------------------------------------------------
    onDragStart(ev, kind) {
      if (!this.canManage) return;
      ev.dataTransfer.setData('node-kind', kind);
    },
    addFromPalette(kind) {
      if (!this.canManage || !this.$refs.graph) return;
      this.$refs.graph.addNodeAtCenter(kind);
    },
    onGraphChange({ nodes, edges }) {
      this.item.nodes = nodes.map((node) => {
        if (node.kind !== 'approval' || node.approval_role_policy !== undefined) return node;
        return {
          ...node,
          approval_permission: USER_PERMISSIONS.runProjectTasks,
          approval_role_policy: this.defaultApprovalRolePolicy(),
        };
      });
      this.item.edges = edges;
      this.markDirty();
      this.history.push({ nodes: this.item.nodes, edges }, this.pendingHistoryKey);
      this.pendingHistoryKey = null;
      this.syncHistoryFlags();
      // Keep the open property panel in sync with the latest model snapshot.
      if (this.selectedNodeId != null) {
        const found = nodes.find((n) => n.id === this.selectedNodeId);
        if (!found) {
          this.selectedNodeId = null;
          this.editingNode = null;
        }
      }
      if (this.editingEdge) {
        const { source_node_id: s, destination_node_id: d } = this.editingEdge;
        const found = edges.find((e) => e.source_node_id === s && e.destination_node_id === d);
        this.editingEdge = found ? { ...found } : null;
      }
    },
    onNodeSelected(nodeId) {
      this.editingEdge = null;
      this.selectedNodeId = nodeId;
      if (nodeId == null) {
        this.editingNode = null;
        return;
      }
      const node = this.item.nodes.find((n) => n.id === nodeId);
      const clone = node ? JSON.parse(JSON.stringify(node)) : null;
      // Define task_params up front so later assignments stay reactive.
      // Approval/note nodes must not carry task params (backend validation).
      if (clone && clone.task_params === undefined) {
        clone.task_params = (clone.kind || 'task') === 'task' ? {} : null;
      }
      if (clone && !Array.isArray(clone.artifact_outputs)) clone.artifact_outputs = [];
      if (clone && !Array.isArray(clone.artifact_inputs)) clone.artifact_inputs = [];
      if (clone && !clone.override_policy) clone.override_policy = {};
      if (clone && clone.kind === 'approval' && !clone.approval_role_policy) {
        clone.approval_role_policy = this.defaultApprovalRolePolicy();
      }
      this.editingNode = clone;
    },
    onConnectionSelected(edge) {
      if (edge == null) {
        this.editingEdge = null;
        return;
      }
      this.selectedNodeId = null;
      this.editingNode = null;
      this.editingEdge = { ...edge };
    },
    onBlocked(reason) {
      const key = reason === 'cycle' ? 'workflowCycleBlocked' : 'workflowSelfEdgeBlocked';
      EventBus.$emit('i-snackbar', { color: 'warning', text: this.$t(key) });
    },
    onKindChanged() {
      const kind = this.editingNode.kind;
      if (kind === 'approval' || kind === 'delay') {
        this.editingNode.template_id = null;
        if (this.$delete) {
          this.$delete(this.editingNode, 'cross_project_template_reference');
        } else {
          delete this.editingNode.cross_project_template_reference;
        }
        this.editingNode.task_params = null;
        this.editingNode.artifact_outputs = [];
        this.editingNode.artifact_inputs = [];
      }
      if (kind === 'approval') {
        this.editingNode.approval_permission = USER_PERMISSIONS.runProjectTasks;
        this.editingNode.approval_timeout_outcome = 'reject';
        this.editingNode.approval_separation_of_duties = false;
        this.editingNode.approval_role_policy = this.defaultApprovalRolePolicy();
      } else {
        this.editingNode.approval_timeout = null;
        this.editingNode.approval_message = null;
        this.editingNode.approval_permission = null;
        this.editingNode.approval_timeout_outcome = null;
        this.editingNode.approval_separation_of_duties = false;
        this.editingNode.approval_role_policy = null;
      }
      if (kind !== 'delay') {
        this.editingNode.delay_seconds = null;
      } else if (this.editingNode.delay_seconds == null) {
        this.editingNode.delay_seconds = 60;
      }
      if (kind === 'task' && !this.editingNode.task_params) {
        this.editingNode.task_params = {};
      }
      this.applyNodeEdit();
    },
    edgeKey(edge) {
      return `edge-${edge.source_node_id}-${edge.destination_node_id}`;
    },
    closePanel() {
      this.editingNode = null;
      this.editingEdge = null;
      this.selectedNodeId = null;
      if (this.$refs.graph) this.$refs.graph.clearSelection();
    },
    focusProblem(problem) {
      if (problem.nodeId == null || !this.$refs.graph) return;
      this.$refs.graph.selectNode(problem.nodeId);
    },
    applyNodeEdit() {
      if (!this.editingNode || !this.$refs.graph) return;
      // Coalesce keystrokes on one node into a single undo step.
      this.pendingHistoryKey = `node-${this.editingNode.id}`;
      this.$refs.graph.syncNode(this.editingNode.id, { ...this.editingNode });
    },
    applyEdgeEdit() {
      if (!this.editingEdge || !this.$refs.graph) return;
      this.pendingHistoryKey = this.edgeKey(this.editingEdge);
      this.$refs.graph.syncEdge({ ...this.editingEdge });
    },
    deleteSelectedNode() {
      if (this.editingNode == null || !this.$refs.graph) return;
      this.$refs.graph.removeSelectedNode(this.editingNode.id);
      this.editingNode = null;
      this.selectedNodeId = null;
    },
    deleteSelectedEdge() {
      if (this.editingEdge == null || !this.$refs.graph) return;
      this.$refs.graph.removeEdge(
        this.editingEdge.source_node_id,
        this.editingEdge.destination_node_id,
      );
      this.editingEdge = null;
    },

    // ---- history ----------------------------------------------------------------
    syncHistoryFlags() {
      this.canUndo = this.history.canUndo();
      this.canRedo = this.history.canRedo();
    },
    applySnapshot(snapshot) {
      if (!snapshot) return;
      this.item.nodes = snapshot.nodes;
      this.item.edges = snapshot.edges;
      this.editingNode = null;
      this.editingEdge = null;
      this.selectedNodeId = null;
      this.syncHistoryFlags();
      this.markDirty();
      this.$nextTick(() => {
        if (this.$refs.graph) this.$refs.graph.reload();
      });
    },
    undo() {
      if (!this.canManage) return;
      this.applySnapshot(this.history.undo());
    },
    redo() {
      if (!this.canManage) return;
      this.applySnapshot(this.history.redo());
    },
    onWindowKeyDown(ev) {
      if (!(ev.ctrlKey || ev.metaKey) || isTypingTarget(ev.target)) return;
      const key = ev.key.toLowerCase();
      if (key === 'z' && ev.shiftKey) {
        ev.preventDefault();
        this.redo();
      } else if (key === 'z') {
        ev.preventDefault();
        this.undo();
      } else if (key === 'y') {
        ev.preventDefault();
        this.redo();
      }
    },

    // ---- unsaved changes guard --------------------------------------------------
    onBeforeUnload(ev) {
      if (!this.dirty) return undefined;
      ev.preventDefault();
      // eslint-disable-next-line no-param-reassign
      ev.returnValue = '';
      return '';
    },
    confirmLeave() {
      const next = this.pendingLeave;
      this.pendingLeave = null;
      if (next) next();
    },
    cancelLeave() {
      const next = this.pendingLeave;
      this.pendingLeave = null;
      if (next) next(false);
    },

    // ---- save -----------------------------------------------------------------
    async save() {
      if (this.clientIssues.length > 0 || this.versionMessageTooLong) return;
      if (!await this.validate(false)) return;
      this.saving = true;
      try {
        const payload = this.payload();
        let saved;
        if (this.isNew) {
          saved = (await axios.post(`/api/project/${this.projectId}/workflows`, payload)).data;
          EventBus.$emit('i-snackbar', { color: 'success', text: this.$t('workflowSaved') });
          this.item = this.prepareItem(saved);
          this.clearSelection();
          this.baseline = this.clone(this.item);
          this.dirty = false;
          this.validationState = 'valid';
          this.graphKey += 1;
          this.skipNextRouteReload = true;
          this.$router.replace(`/project/${this.projectId}/workflows/${saved.id}/edit`);
        } else {
          saved = (await axios.put(
            `/api/project/${this.projectId}/workflows/${this.workflowId}`,
            payload,
          )).data;
          this.item = this.prepareItem(saved);
          this.clearSelection();
          this.baseline = this.clone(this.item);
          this.dirty = false;
          this.validationState = 'valid';
          this.graphKey += 1;
          EventBus.$emit('i-snackbar', { color: 'success', text: this.$t('workflowSaved') });
        }
      } catch (err) {
        if (err.response?.status === 409
            && err.response?.data?.code === 'WORKFLOW_REVISION_CONFLICT') {
          this.conflict = err.response.data;
          EventBus.$emit('i-snackbar', {
            color: 'warning', text: this.$t('workflowConflictMessage'),
          });
          return;
        }
        if (err.response?.status === 422 && Array.isArray(err.response?.data?.issues)) {
          this.validationIssues = err.response.data.issues;
          this.validationState = 'invalid';
          return;
        }
        EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
      } finally {
        this.saving = false;
      }
    },
  },
};
</script>

<style lang="scss" scoped>

$worklow_pallete_width_collapsed: 60px;

.WorkflowEditor {
  // Inline, borderless name editor in the toolbar title.
  &__title {
    overflow: visible;
  }

  // Inline name editor: looks like a title, but the pencil + hover/focus
  // affordances make it clear it is editable. The field auto-sizes to the
  // width of its text via the grid-sizer trick below.
  &__nameWrap {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    max-width: 60vw;
    padding: 2px 8px;
    border-radius: 4px;
    background: rgba(127, 127, 127, 0.12);
    transition: background-color 0.15s ease, border-color 0.15s ease;

    &--disabled {
      pointer-events: none;
      opacity: 0.7;
    }
  }

  &__nameIcon {
    opacity: 0.6;
    flex: 0 0 auto;
  }

  &__nameWrap:focus-within &__nameIcon {
    opacity: 1;
  }

  // The sizer ::after mirrors the value and gives the grid cell the text width;
  // the input shares the same cell, so it is exactly as wide as the text.
  &__nameSizer {
    display: inline-grid;
    align-items: center;
    min-width: 0;

    &::after,
    & > .WorkflowEditor__nameField {
      grid-area: 1 / 1;
      width: auto;
      min-width: 1ch;
      font: inherit;
      font-weight: 500;
      letter-spacing: inherit;
      padding: 0;
      margin: 0;
      border: 0;
      background: transparent;
      white-space: pre;
    }

    &::after {
      content: attr(data-value);
      visibility: hidden;
    }
  }

  &__nameField {
    color: inherit;
    outline: none;

    &::placeholder {
      color: inherit;
      opacity: 0.5;
    }
  }

  &__body {
    display: flex;
    flex: 1 1 auto;
    min-height: 0;
    height: calc(100vh - 65px);
  }

  &__side {
    width: 280px;
    flex: 0 0 280px;
    overflow-y: auto;
    border-right: 1px solid rgba(127, 127, 127, 0.2);

    // The left panel hosts the collapse tab protruding over the canvas, so it
    // must not clip overflow itself — scrolling moves to __sideScroll. z-index
    // lifts the tab above the (positioned) canvas that follows in the DOM.
    &--left {
      position: relative;
      overflow: visible;
      z-index: 1;
    }

    &--right {
      width: 360px;
      flex: 0 0 360px;
      border-right: none;
      border-left: 1px solid rgba(127, 127, 127, 0.2);
    }

    &--collapsed {
      width: $worklow_pallete_width_collapsed;
      flex: 0 0 $worklow_pallete_width_collapsed;
    }
  }

  &__sideScroll {
    height: 100%;
    overflow-y: auto;
    overflow-x: hidden;
  }

  // Collapse/expand handle: a tab sticking out of the panel's right edge,
  // vertically centered.
  &__sideToggle {
    position: absolute;
    top: 50%;
    right: -20px;
    transform: translateY(-50%);
    width: 20px;
    height: 48px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    border: 1px solid rgba(127, 127, 127, 0.2);
    border-left: none;
    border-radius: 0 8px 8px 0;
    cursor: pointer;
    background: inherit;
    background: white;

  }

  &__canvas {
    flex: 1 1 auto;
    min-width: 0;
    position: relative;
  }

  &__paletteItem {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 8px;
    margin-bottom: 8px;
    border: 1px solid rgba(127, 127, 127, 0.3);
    border-radius: 10px;
    cursor: grab;
    user-select: none;
    font-size: 13px;
    transition: border-color 0.12s ease, box-shadow 0.12s ease;

    &:hover {
      border-color: rgba(127, 127, 127, 0.6);
      box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
    }

    &:active {
      cursor: grabbing;
    }
  }

  &__paletteTile {
    flex: 0 0 30px;
    width: 30px;
    height: 30px;
    border-radius: 8px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: rgba(127, 127, 127, 0.12);

    &--approval {
      background: rgba(171, 71, 188, 0.14);
    }

    &--delay {
      background: rgba(255, 152, 0, 0.16);
    }

    &--note {
      background: rgba(230, 216, 115, 0.35);
    }
  }

  &__paletteText {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  &__side--collapsed &__paletteItem {
    justify-content: center;
    padding: 0;
  }

  &__side--collapsed &__paletteTile {
    width: 33px;
    min-width: 33px;
    height: 33px;
    border-radius: 9px;
  }
}

@media (max-width: 959px) {
  .WorkflowEditor {
    &__toolbar {
      height: auto !important;

      ::v-deep .v-toolbar__content {
        height: auto !important;
        min-height: 64px;
        flex-wrap: wrap;
        row-gap: 8px;
      }
    }

    &__title {
      flex: 1 1 calc(100% - 70px);
      min-width: 0;
      max-width: calc(100% - 70px);
      flex-wrap: wrap;
      gap: 4px;
      font-size: 16px;
    }

    &__nameWrap {
      max-width: 100%;
      min-width: 0;
    }

    &__sideToggle { display: none; }

    &__body {
      flex-direction: column;
      height: auto;
      min-height: calc(100vh - 65px);
    }

    &__side {
      width: 100%;
      flex: 0 0 auto;
      overflow: visible;
      border-right: none;
      border-bottom: 1px solid rgba(127, 127, 127, 0.2);

      &--right {
        border-left: none;
        border-top: 1px solid rgba(127, 127, 127, 0.2);
      }
    }

    &__canvas {
      flex: 0 0 420px;
      width: 100%;
      min-height: 420px;
    }
  }
}
.theme--dark {
  .WorkflowEditor__sideToggle {
    background: #1e1e1e;
  }
}

</style>
