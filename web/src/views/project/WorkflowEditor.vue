<template>
  <div>
    <v-toolbar flat>
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
      </v-toolbar-title>

      <v-spacer></v-spacer>

      <v-btn icon :title="$t('workflowToolbarZoomOut')" @click="zoomOut()">
        <v-icon>mdi-magnify-minus-outline</v-icon>
      </v-btn>
      <v-btn icon :title="$t('workflowToolbarZoomIn')" @click="zoomIn()">
        <v-icon>mdi-magnify-plus-outline</v-icon>
      </v-btn>
      <v-btn icon :title="$t('workflowToolbarFit')" @click="zoomReset()" class="mr-4">
        <v-icon>mdi-fit-to-page-outline</v-icon>
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
        :disabled="!canManage || saving || validating || clientIssues.length > 0"
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
          @click="sideCollapsed = !sideCollapsed"
        >
          <v-icon small>
            {{ sideCollapsed ? 'mdi-chevron-right' : 'mdi-chevron-left' }}
          </v-icon>
        </button>

        <div class="WorkflowEditor__sideScroll">
          <template v-if="!sideCollapsed">
            <div class="pa-3">
              <v-text-field
                v-model="item.start_version"
                :label="$t('startVersion')"
                :hint="$t('workflowStartVersionHint')"
                persistent-hint
                :disabled="!canManage"
                outlined
                dense
                @input="markDirty"
              />
              <v-text-field
                v-model.number="item.max_parallel_tasks"
                type="number"
                min="1"
                max="32"
                :label="$t('workflowMaxParallelTasks')"
                :hint="$t('workflowMaxParallelTasksHint')"
                persistent-hint
                :disabled="!canManage"
                outlined
                dense
                @input="markDirty"
              />
              <v-select
                v-model="item.access_policy.view_role_ids"
                :items="workflowRoleOptions"
                item-value="value"
                item-text="text"
                :label="$t('workflowViewRoles')"
                :hint="$t('workflowRoleRestrictionHint')"
                persistent-hint
                multiple
                chips
                small-chips
                deletable-chips
                :disabled="!canAdminister"
                outlined
                dense
                @change="markDirty"
              />
              <v-select
                v-model="item.access_policy.start_role_ids"
                :items="workflowRoleOptions"
                item-value="value"
                item-text="text"
                :label="$t('workflowStartRoles')"
                :hint="$t('workflowRoleRestrictionHint')"
                persistent-hint
                multiple
                chips
                small-chips
                deletable-chips
                :disabled="!canAdminister"
                outlined
                dense
                @change="markDirty"
              />
              <WorkflowParameterEditor
                v-model="item.parameters"
                :project-id="projectId"
                :disabled="!canManage"
                @input="markDirty"
              />
            </div>

            <v-divider />
          </template>

          <div class="pa-3">
            <template v-if="!sideCollapsed">
              <div class="text-subtitle-2 mb-2">{{ $t('workflowEditorPalette') }}</div>
              <div class="text-caption text--secondary mb-2">
                {{ $t('workflowDragToCanvasHint') }}
              </div>
            </template>
            <div
              class="WorkflowEditor__paletteItem WorkflowEditor__paletteItem--task"
              :draggable="canManage"
              :title="sideCollapsed ? $t('workflowPaletteTaskNode') : null"
              @dragstart="onDragStart($event, 'task')"
            >
              <v-icon small :left="!sideCollapsed">mdi-cog</v-icon>
              <template v-if="!sideCollapsed">{{ $t('workflowPaletteTaskNode') }}</template>
            </div>
            <div
              class="WorkflowEditor__paletteItem WorkflowEditor__paletteItem--approval"
              :draggable="canManage"
              :title="sideCollapsed ? $t('workflowPaletteApprovalNode') : null"
              @dragstart="onDragStart($event, 'approval')"
            >
              <v-icon small :left="!sideCollapsed">mdi-account-check</v-icon>
              <template v-if="!sideCollapsed">{{ $t('workflowPaletteApprovalNode') }}</template>
            </div>
            <div
              class="WorkflowEditor__paletteItem WorkflowEditor__paletteItem--delay"
              :draggable="canManage"
              :title="sideCollapsed ? $t('workflowPaletteDelayNode') : null"
              @dragstart="onDragStart($event, 'delay')"
            >
              <v-icon small :left="!sideCollapsed">mdi-timer-outline</v-icon>
              <template v-if="!sideCollapsed">{{ $t('workflowPaletteDelayNode') }}</template>
            </div>

            <div
              class="WorkflowEditor__paletteItem WorkflowEditor__paletteItem--note"
              :draggable="canManage"
              :title="sideCollapsed ? $t('workflowPaletteNoteNode') : null"
              @dragstart="onDragStart($event, 'note')"
            >
              <v-icon small :left="!sideCollapsed">mdi-note-text-outline</v-icon>
              <template v-if="!sideCollapsed">{{ $t('workflowPaletteNoteNode') }}</template>
            </div>
          </div>

          <v-divider />

          <div class="pa-3">
            <template v-if="!sideCollapsed">
              <div class="text-subtitle-2 mb-1">{{ $t('workflowProblemsPanelTitle') }}</div>
              <v-alert
                v-if="problems.length === 0 && validationState === 'valid'"
                type="success"
                text
                dense
                class="mb-0"
              >
                {{ $t('workflowValidationPassed') }}
              </v-alert>
              <v-alert
                v-else-if="problems.length === 0"
                type="info"
                text
                dense
                class="mb-0"
              >
                {{ $t('workflowValidationNotRun') }}
              </v-alert>
              <v-alert
                v-for="(p, i) in problems"
                :key="`${p.code}-${p.path || i}`"
                type="warning"
                text
                dense
                class="mb-1"
              >{{ problemText(p) }}
              </v-alert
              >
            </template>
            <div v-else class="d-flex flex-column align-center">
              <v-tooltip
                v-if="problems.length === 0 && validationState === 'valid'"
                right
                max-width="320"
                transition="fade-transition"
              >
                <template v-slot:activator="{ on, attrs }">
                  <v-icon color="success" v-bind="attrs" v-on="on">mdi-check-circle</v-icon>
                </template>
                <span>{{ $t('workflowValidationPassed') }}</span>
              </v-tooltip>
              <v-tooltip
                v-else-if="problems.length === 0"
                right
                max-width="320"
                transition="fade-transition"
              >
                <template v-slot:activator="{ on, attrs }">
                  <v-icon color="info" v-bind="attrs" v-on="on">mdi-information</v-icon>
                </template>
                <span>{{ $t('workflowValidationNotRun') }}</span>
              </v-tooltip>
              <template v-else>
                <v-tooltip
                  v-for="(p, i) in problems"
                  :key="`${p.code}-${p.path || i}`"
                  right
                  max-width="320"
                  transition="fade-transition"
                >
                  <template v-slot:activator="{ on, attrs }">
                    <v-icon color="warning" class="mb-1" v-bind="attrs" v-on="on">
                      mdi-alert
                    </v-icon>
                  </template>
                  <span>{{ problemText(p) }}</span>
                </v-tooltip>
              </template>
            </div>
          </div>
        </div>
      </div>

      <!-- Canvas -->
      <div class="WorkflowEditor__canvas">
        <WorkflowGraph
          ref="graph"
          :key="graphKey"
          :nodes="item.nodes"
          :edges="item.edges"
          :templates="templates"
          :editable="canManage"
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
              v-model="editingNode.template_id"
              :items="templates"
              item-value="id"
              item-text="name"
              :label="$t('taskTemplate')"
              :disabled="!canManage"
              outlined
              dense
              hide-details="auto"
              class="mb-5"
              @change="applyNodeEdit"
            />

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

            <template v-if="editingNode.kind === 'task'">
              <div
                class="d-flex align-center mb-2"
                data-testid="workflow-artifact-outputs"
              >
                <span class="text-subtitle-2">{{ $t('workflowArtifactOutputs') }}</span>
                <v-spacer />
                <v-btn
                  icon
                  small
                  :title="$t('workflowArtifactAddOutput')"
                  :disabled="!canManage"
                  @click="addArtifactOutput"
                >
                  <v-icon small>mdi-plus</v-icon>
                </v-btn>
              </div>

              <v-card
                v-for="(output, index) in editingNode.artifact_outputs"
                :key="`artifact-output-${index}`"
                outlined
                class="pa-2 mb-2"
              >
                <div class="d-flex align-start">
                  <v-text-field
                    v-model="output.name"
                    :label="$t('workflowArtifactName')"
                    :disabled="!canManage"
                    dense
                    hide-details="auto"
                    class="mr-1"
                    @input="applyNodeEdit"
                  />
                  <v-btn
                    icon
                    small
                    :title="$t('workflowArtifactRemoveOutput')"
                    :disabled="!canManage"
                    @click="removeArtifactOutput(index)"
                  >
                    <v-icon small>mdi-close</v-icon>
                  </v-btn>
                </div>
                <v-select
                  :value="output.schema.type"
                  :items="artifactOutputTypes"
                  item-value="value"
                  item-text="text"
                  :label="$t('workflowArtifactSchema')"
                  :disabled="!canManage"
                  dense
                  hide-details="auto"
                  @change="setArtifactOutputType(index, $event)"
                />
                <v-text-field
                  v-model.number="output.max_bytes"
                  type="number"
                  min="1"
                  max="65536"
                  :label="$t('workflowArtifactMaxBytes')"
                  :disabled="!canManage"
                  dense
                  hide-details="auto"
                  @input="applyNodeEdit"
                />
                <v-switch
                  v-model="output.sensitive"
                  :label="$t('workflowArtifactSensitive')"
                  :disabled="!canManage"
                  dense
                  hide-details
                  class="mt-1"
                  @change="applyNodeEdit"
                />
              </v-card>

              <div
                class="d-flex align-center mt-4 mb-2"
                data-testid="workflow-artifact-inputs"
              >
                <span class="text-subtitle-2">{{ $t('workflowArtifactInputs') }}</span>
                <v-spacer />
                <v-btn
                  icon
                  small
                  :title="$t('workflowArtifactAddInput')"
                  :disabled="!canManage || reachableArtifactOutputs.length === 0"
                  @click="addArtifactInput"
                >
                  <v-icon small>mdi-plus</v-icon>
                </v-btn>
              </div>

              <div
                v-if="reachableArtifactOutputs.length === 0"
                class="text-caption text--secondary mb-3"
              >{{ $t('workflowArtifactNoReachableOutputs') }}</div>

              <v-card
                v-for="(input, index) in editingNode.artifact_inputs"
                :key="`artifact-input-${index}`"
                outlined
                class="pa-2 mb-2"
              >
                <div class="d-flex align-start">
                  <v-text-field
                    v-model="input.name"
                    :label="$t('workflowArtifactInputName')"
                    :disabled="!canManage"
                    dense
                    hide-details="auto"
                    class="mr-1"
                    @input="applyNodeEdit"
                  />
                  <v-btn
                    icon
                    small
                    :title="$t('workflowArtifactRemoveInput')"
                    :disabled="!canManage"
                    @click="removeArtifactInput(index)"
                  >
                    <v-icon small>mdi-close</v-icon>
                  </v-btn>
                </div>
                <v-select
                  :value="artifactReferenceKey(input)"
                  :items="reachableArtifactOutputs"
                  item-value="value"
                  item-text="text"
                  :label="$t('workflowArtifactSource')"
                  :disabled="!canManage"
                  dense
                  hide-details="auto"
                  @change="setArtifactReference(index, $event)"
                />
                <v-switch
                  v-model="input.required"
                  :label="$t('workflowArtifactRequired')"
                  :disabled="!canManage"
                  dense
                  hide-details
                  class="mt-1"
                  @change="applyNodeEdit"
                />
              </v-card>
            </template>

            <template v-if="editingNode.kind === 'approval'">
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
              <v-select
                v-model="editingNode.approval_role_policy.role_ids"
                :items="workflowRoleOptions"
                item-value="value"
                item-text="text"
                :label="$t('workflowApprovalRoles')"
                :disabled="!canAdminister"
                multiple
                chips
                small-chips
                deletable-chips
                outlined
                dense
                hide-details="auto"
                class="mb-2"
                @change="onApprovalPolicyChanged"
              />
              <v-select
                v-model="editingNode.approval_role_policy.mode"
                :items="approvalRoleModeOptions"
                item-value="value"
                item-text="text"
                :label="$t('workflowApprovalRoleMode')"
                :disabled="!canAdminister"
                outlined
                dense
                hide-details="auto"
                class="mb-2"
                @change="onApprovalPolicyChanged"
              />
              <v-text-field
                v-model.number="editingNode.approval_role_policy.minimum_distinct_approvers"
                type="number"
                min="1"
                :label="$t('workflowApprovalMinimumApprovers')"
                :disabled="!canAdminister"
                outlined
                dense
                hide-details="auto"
                class="mb-2"
                @change="onApprovalPolicyChanged"
              />
              <v-select
                v-model="editingNode.approval_timeout_outcome"
                :items="approvalTimeoutOutcomeOptions"
                item-value="value"
                item-text="text"
                :label="$t('workflowApprovalTimeoutOutcome')"
                :disabled="!canManage"
                outlined
                dense
                hide-details="auto"
                class="mb-2"
                @change="applyNodeEdit"
              />
              <v-switch
                v-model="editingNode.approval_role_policy.initiator_separation"
                :label="$t('workflowApprovalSeparationOfDuties')"
                :disabled="!canAdminister"
                dense
                hide-details
                class="mt-0"
                @change="onApprovalPolicyChanged"
              />
            </template>
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
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/workflow-editor';

import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import TaskParamsForm from '@/components/TaskParamsForm.vue';
import WorkflowGraph from '@/components/WorkflowGraph.vue';
import WorkflowNodeOverridePolicyEditor from '@/components/WorkflowNodeOverridePolicyEditor.vue';
import WorkflowParameterEditor from '@/components/WorkflowParameterEditor.vue';
import ProjectMixin from '@/components/ProjectMixin';
import PermissionsCheck from '@/components/PermissionsCheck';
import { USER_PERMISSIONS } from '@/lib/constants';
import { layoutWorkflowNodes, needsAutoLayout } from '@/lib/workflowLayout';
import { WORKFLOW_DEFINITION_VERSION } from '@/lib/workflowValidation';

export default {
  components: {
    TaskParamsForm,
    WorkflowGraph,
    WorkflowNodeOverridePolicyEditor,
    WorkflowParameterEditor,
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
      validating: false,
      dirty: false,
      validationState: 'idle',
      validationIssues: [],
      conflict: null,
      graphKey: 0,
      // Set before navigating new -> /edit after a create, so the route watcher
      // does not reload (which would reset the selection and rebuild the canvas).
      skipNextRouteReload: false,
      selectedNodeId: null,
      editingNode: null,
      editingEdge: null,
      sideCollapsed: false,
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
      return this.templates.find((t) => t.id === this.editingNode.template_id) || null;
    },
    kindOptions() {
      return [
        { value: 'task', text: this.$t('workflowNodeKindTask') },
        { value: 'approval', text: this.$t('workflowNodeKindApproval') },
        { value: 'delay', text: this.$t('workflowNodeKindDelay') },
      ];
    },
    convergenceOptions() {
      return [
        { value: 'all', text: this.$t('workflowConvergenceAll') },
        { value: 'any', text: this.$t('workflowConvergenceAny') },
      ];
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
  },
  watch: {
    '$route.params.workflowId': function reloadOnRoute() {
      if (this.skipNextRouteReload) {
        this.skipNextRouteReload = false;
        return;
      }
      this.loadData();
    },
  },
  async created() {
    [this.templates, this.projectRoles] = await Promise.all([
      this.loadProjectResources('templates'),
      this.loadEndpoint(`/api/project/${this.projectId}/roles/all`),
    ]);
    await this.loadData();
  },
  methods: {
    ...enhancedMethods,
    showDrawer() {
      EventBus.$emit('i-show-drawer');
    },
    getNewItem() {
      return {
        name: '',
        description: '',
        definition_version: WORKFLOW_DEFINITION_VERSION,
        revision: 0,
        max_parallel_tasks: 4,
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
        }
      } catch (err) {
        EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
        return;
      }
      this.baseline = this.clone(this.item);
      this.dirty = false;
      // Force a clean canvas rebuild matching the freshly loaded model.
      this.graphKey += 1;
    },
    // Seed positions for legacy workflows whose nodes were created before the
    // graphical editor (all coordinates 0) so the computed layout persists on
    // the next save. Uses the same algorithm as the read-only run view.
    autoLayout() {
      const nodes = this.item.nodes;
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
      // Keep the open property panel in sync with the latest model snapshot.
      if (this.selectedNodeId != null) {
        const found = nodes.find((n) => n.id === this.selectedNodeId);
        if (!found) {
          this.selectedNodeId = null;
          this.editingNode = null;
        }
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
    applyNodeEdit() {
      if (!this.editingNode || !this.$refs.graph) return;
      this.$refs.graph.syncNode(this.editingNode.id, { ...this.editingNode });
    },
    applyEdgeEdit() {
      if (!this.editingEdge || !this.$refs.graph) return;
      this.$refs.graph.syncEdge({ ...this.editingEdge });
    },
    deleteSelectedNode() {
      if (this.editingNode == null || !this.$refs.graph) return;
      this.$refs.graph.removeSelectedNode(this.editingNode.id);
      this.editingNode = null;
      this.selectedNodeId = null;
    },
    zoomIn() {
      this.$refs.graph?.zoomIn();
    },
    zoomOut() {
      this.$refs.graph?.zoomOut();
    },
    zoomReset() {
      this.$refs.graph?.zoomReset();
    },

    // ---- save -----------------------------------------------------------------
    async save() {
      if (this.clientIssues.length > 0) return;
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

  // Inline name editor: looks like a title, but the pencil + dashed underline +
  // hover/focus affordances make it clear it is editable. The field auto-sizes
  // to the width of its text via the grid-sizer trick below.
  &__nameWrap {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    max-width: 60vw;
    padding: 2px 8px;
    border-radius: 4px;
    background: rgba(127, 127, 127, 0.12);
    //border-bottom: 2px solid transparent;
    transition: background-color 0.15s ease,
    border-color 0.15s ease;

    &:hover {
      background: rgba(127, 127, 127, 0.12);
      //border-bottom-color: rgba(127, 127, 127, 0.9);
    }

    &:focus-within {
      background: rgba(127, 127, 127, 0.12);
      //border-bottom: 2px solid var(--v-primary-base, #1976d2);
    }

    &--disabled {
      pointer-events: none;
      //border-bottom-style: solid;
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
  }

  &__canvas {
    flex: 1 1 auto;
    min-width: 0;
    position: relative;
  }

  &__paletteItem {
    display: flex;
    align-items: center;
    padding: 8px 10px;
    margin-bottom: 8px;
    border: 1px dashed rgba(127, 127, 127, 0.5);
    border-radius: 6px;
    cursor: grab;
    user-select: none;
    font-size: 13px;

    &--task {
      border-left: 3px solid #2196f3;
    }

    &--approval {
      border-left: 3px solid #ab47bc;
    }

    &--delay {
      border-left: 3px solid #ff9800;
    }

    &--note {
      border-left: 3px solid #e6d873;
    }
  }

  &__side--collapsed &__paletteItem {
    padding-left: 8px;
  }
}
</style>
