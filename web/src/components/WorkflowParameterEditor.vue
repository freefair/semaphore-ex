<template>
  <v-expansion-panels
    flat
    variant="accordion"
    class="WorkflowParameterEditor"
    data-testid="workflow-parameters"
  >
    <v-expansion-panel>
      <v-expansion-panel-title class="px-0 py-2">
        {{ $t('workflowParameters') }} ({{ items.length }})
      </v-expansion-panel-title>
      <v-expansion-panel-text class="WorkflowParameterEditor__content">
        <v-card
          v-for="(parameter, index) in items"
          :key="`workflow-parameter-${index}`"
          variant="outlined"
          class="pa-2 mb-2"
        >
          <div class="d-flex align-start">
            <v-text-field
              v-model="parameter.name"
              :label="$t('name')"
              :disabled="disabled"
              density="compact"
              hide-details="auto"
              class="mr-1"
              @update:model-value="emitValue"
            />
            <v-btn icon size="small" :disabled="disabled" @click="removeParameter(index)">
              <v-icon size="small">mdi-close</v-icon>
            </v-btn>
          </div>
          <v-text-field
            v-model="parameter.description"
            :label="$t('description')"
            :disabled="disabled"
            density="compact"
            hide-details="auto"
            @update:model-value="emitValue"
          />
          <v-select
            :model-value="parameter.type"
            :items="typeOptions"
            item-value="value"
            item-title="text"
            :label="$t('type')"
            :disabled="disabled"
            density="compact"
            hide-details="auto"
            @update:model-value="setType(index, $event)"
          />

          <v-text-field
            v-if="parameter.type === 'string'"
            :model-value="parameter.default"
            :label="$t('default')"
            :disabled="disabled"
            clearable
            density="compact"
            hide-details="auto"
            @update:model-value="setDefault(index, $event)"
          />
          <div v-if="parameter.type === 'string'" class="d-flex">
            <v-text-field
              v-model.number="parameter.min_length"
              type="number"
              min="0"
              :label="$t('workflowMinimumLength')"
              :disabled="disabled"
              density="compact"
              hide-details="auto"
              class="mr-1"
              @update:model-value="emitValue"
            />
            <v-text-field
              v-model.number="parameter.max_length"
              type="number"
              min="0"
              :max="maxStringBytes"
              :label="$t('workflowMaximumLength')"
              :disabled="disabled"
              density="compact"
              hide-details="auto"
              @update:model-value="emitValue"
            />
          </div>

          <template v-else-if="parameter.type === 'integer'">
            <v-text-field
              :model-value="parameter.default"
              type="number"
              :label="$t('default')"
              :disabled="disabled"
              clearable
              density="compact"
              hide-details="auto"
              @update:model-value="setInteger(index, 'default', $event)"
            />
            <div class="d-flex">
              <v-text-field
                :model-value="parameter.minimum"
                type="number"
                :label="$t('workflowMinimum')"
                :disabled="disabled"
                clearable
                density="compact"
                hide-details="auto"
                class="mr-1"
                @update:model-value="setInteger(index, 'minimum', $event)"
              />
              <v-text-field
                :model-value="parameter.maximum"
                type="number"
                :label="$t('workflowMaximum')"
                :disabled="disabled"
                clearable
                density="compact"
                hide-details="auto"
                @update:model-value="setInteger(index, 'maximum', $event)"
              />
            </div>
          </template>

          <v-select
            v-else-if="parameter.type === 'boolean'"
            :model-value="parameter.default"
            :items="booleanOptions"
            item-value="value"
            item-title="text"
            :label="$t('default')"
            :disabled="disabled"
            clearable
            density="compact"
            hide-details="auto"
            @update:model-value="setDefault(index, $event)"
          />

          <template v-else-if="parameter.type === 'enumeration'">
            <v-text-field
              :model-value="(parameter.options || []).join(', ')"
              :label="$t('workflowEnumerationOptions')"
              :disabled="disabled"
              density="compact"
              hide-details="auto"
              @update:model-value="setOptions(index, $event)"
            />
            <v-select
              :model-value="parameter.default"
              :items="parameter.options || []"
              :label="$t('default')"
              :disabled="disabled"
              clearable
              density="compact"
              hide-details="auto"
              @update:model-value="setDefault(index, $event)"
            />
          </template>

          <template v-else-if="parameter.type === 'secret_reference'">
            <v-autocomplete
              :model-value="secretOptionKeys(parameter)"
              :items="credentials"
              item-value="value"
              item-title="text"
              :label="$t('workflowApprovedCredentials')"
              :disabled="disabled"
              multiple
              chips

              density="compact"
              hide-details="auto"
              @update:model-value="setSecretOptions(index, $event)"
            />
            <v-select
              :model-value="credentialReferenceKey(parameter.default)"
              :items="secretOptionItems(parameter)"
              item-value="value"
              item-title="text"
              :label="$t('default')"
              :disabled="disabled"
              clearable
              density="compact"
              hide-details="auto"
              @update:model-value="setSecretDefault(index, $event)"
            />
          </template>

          <v-switch
            v-model="parameter.required"
            :label="$t('required')"
            :disabled="disabled"
            density="compact"
            hide-details
            class="mt-1"
            @update:model-value="emitValue"
          />
        </v-card>

        <v-menu location="bottom">
          <template v-slot:activator="{ props }">
            <v-btn
              variant="text"
              size="small"
              color="primary"
              :disabled="disabled"
              v-bind="props"
            >
              <v-icon start size="small">mdi-plus</v-icon>
              {{ $t('workflowAddParameter') }}
            </v-btn>
          </template>
          <v-list density="compact">
            <v-list-item
              v-for="option in typeOptions"
              :key="option.value"
              @click="addParameter(option.value)"
            >
              <v-list-item-title>{{ option.text }}</v-list-item-title>
            </v-list-item>
          </v-list>
        </v-menu>
      </v-expansion-panel-text>
    </v-expansion-panel>
  </v-expansion-panels>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import {
  credentialOptionItems,
  credentialReferenceFromKey,
  credentialReferenceKey,
} from '@/lib/workflow-credential-references';

export default {
  emits: ['update:modelValue'],
  props: {
    modelValue: { type: Array, default: () => [] },
    projectId: { type: Number, required: true },
    disabled: Boolean,
  },
  data() {
    return {
      items: [],
      credentials: [],
      maxStringBytes: 4096,
    };
  },
  computed: {
    typeOptions() {
      return [
        { value: 'string', text: this.$t('workflowParameterTypeString') },
        { value: 'integer', text: this.$t('workflowParameterTypeInteger') },
        { value: 'boolean', text: this.$t('workflowParameterTypeBoolean') },
        { value: 'enumeration', text: this.$t('workflowParameterTypeEnumeration') },
        { value: 'secret_reference', text: this.$t('workflowParameterTypeSecretReference') },
      ];
    },
    booleanOptions() {
      return [
        { value: true, text: this.$t('yes') },
        { value: false, text: this.$t('no') },
      ];
    },
  },
  watch: {
    modelValue: {
      immediate: true,
      deep: true,
      handler(value) {
        const incoming = JSON.stringify(value || []);
        if (incoming !== JSON.stringify(this.items)) this.items = JSON.parse(incoming);
      },
    },
  },
  async created() {
    try {
      const response = await axios.get(`/api/project/${this.projectId}/keys`);
      this.credentials = (response.data || []).filter(
        (key) => key.type === 'string' && !key.owner,
      ).map((key) => ({
        value: `access_key:${key.id}`,
        text: key.name,
        reference: { access_key_id: key.id },
      }));
    } catch (err) {
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
    }
    try {
      const response = await axios.get(
        `/api/project/${this.projectId}/granted-credentials?count=100&offset=0`,
      );
      this.credentials.push(...(response.data || []).map((credential) => ({
        value: `global_credential:${credential.credential_id}`,
        text: `Global · ${credential.display_name}`,
        reference: { global_credential_id: credential.credential_id },
      })));
    } catch (err) {
      if (err?.response?.status !== 403) {
        EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
      }
    }
  },
  methods: {
    newParameter(type) {
      const parameter = {
        name: '', description: '', type, required: false,
      };
      if (type === 'enumeration') parameter.options = [];
      if (type === 'secret_reference') parameter.secret_options = [];
      return parameter;
    },
    emitValue() {
      this.$emit('update:modelValue', JSON.parse(JSON.stringify(this.items)));
    },
    addParameter(type) {
      this.items.push(this.newParameter(type));
      this.emitValue();
    },
    removeParameter(index) {
      this.items.splice(index, 1);
      this.emitValue();
    },
    setType(index, type) {
      const current = this.items[index];
      this.items[index] = {
        ...this.newParameter(type),
        name: current.name,
        description: current.description,
        required: current.required,
      };
      this.emitValue();
    },
    setDefault(index, value) {
      if (value === null || value === undefined) delete this.items[index].default;
      else this.items[index].default = value;
      this.emitValue();
    },
    setInteger(index, field, value) {
      if (value === null || value === undefined || value === '') {
        delete this.items[index][field];
      } else {
        this.items[index][field] = Number(value);
      }
      this.emitValue();
    },
    setOptions(index, value) {
      const options = value.split(',').map((entry) => entry.trim()).filter(Boolean);
      this.items[index].options = options;
      if (this.items[index].default && !options.includes(this.items[index].default)) {
        delete this.items[index].default;
      }
      this.emitValue();
    },
    credentialReferenceKey,
    secretOptionItems(parameter) {
      return credentialOptionItems(parameter.secret_options);
    },
    secretOptionKeys(parameter) {
      return (parameter.secret_options || []).map(credentialReferenceKey).filter(Boolean);
    },
    setSecretOptions(index, keys) {
      const options = keys.map((key) => {
        const credential = this.credentials.find((entry) => entry.value === key);
        const reference = credential?.reference || credentialReferenceFromKey(key);
        return { ...reference, label: credential?.text || key };
      }).filter((option) => credentialReferenceKey(option));
      this.items[index].secret_options = options;
      const defaultKey = credentialReferenceKey(this.items[index].default);
      if (defaultKey && !keys.includes(defaultKey)) delete this.items[index].default;
      this.emitValue();
    },
    setSecretDefault(index, key) {
      this.setDefault(index, key == null ? null : credentialReferenceFromKey(key));
    },
  },
};
</script>

<style scoped>
.WorkflowParameterEditor__content {
  margin: 0 -16px;
}
</style>
