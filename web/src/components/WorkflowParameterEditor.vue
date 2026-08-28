<template>
  <v-expansion-panels
    flat
    accordion
    class="WorkflowParameterEditor"
    data-testid="workflow-parameters"
  >
    <v-expansion-panel>
      <v-expansion-panel-header class="px-0 py-2">
        {{ $t('workflowParameters') }} ({{ items.length }})
      </v-expansion-panel-header>
      <v-expansion-panel-content class="WorkflowParameterEditor__content">
        <v-card
          v-for="(parameter, index) in items"
          :key="`workflow-parameter-${index}`"
          outlined
          class="pa-2 mb-2"
        >
          <div class="d-flex align-start">
            <v-text-field
              v-model="parameter.name"
              :label="$t('name')"
              :disabled="disabled"
              dense
              hide-details="auto"
              class="mr-1"
              @input="emitValue"
            />
            <v-btn icon small :disabled="disabled" @click="removeParameter(index)">
              <v-icon small>mdi-close</v-icon>
            </v-btn>
          </div>
          <v-text-field
            v-model="parameter.description"
            :label="$t('description')"
            :disabled="disabled"
            dense
            hide-details="auto"
            @input="emitValue"
          />
          <v-select
            :value="parameter.type"
            :items="typeOptions"
            item-value="value"
            item-text="text"
            :label="$t('type')"
            :disabled="disabled"
            dense
            hide-details="auto"
            @change="setType(index, $event)"
          />

          <v-text-field
            v-if="parameter.type === 'string'"
            :value="parameter.default"
            :label="$t('default')"
            :disabled="disabled"
            clearable
            dense
            hide-details="auto"
            @input="setDefault(index, $event)"
          />
          <div v-if="parameter.type === 'string'" class="d-flex">
            <v-text-field
              v-model.number="parameter.min_length"
              type="number"
              min="0"
              :label="$t('workflowMinimumLength')"
              :disabled="disabled"
              dense
              hide-details="auto"
              class="mr-1"
              @input="emitValue"
            />
            <v-text-field
              v-model.number="parameter.max_length"
              type="number"
              min="0"
              :max="maxStringBytes"
              :label="$t('workflowMaximumLength')"
              :disabled="disabled"
              dense
              hide-details="auto"
              @input="emitValue"
            />
          </div>

          <template v-else-if="parameter.type === 'integer'">
            <v-text-field
              :value="parameter.default"
              type="number"
              :label="$t('default')"
              :disabled="disabled"
              clearable
              dense
              hide-details="auto"
              @input="setInteger(index, 'default', $event)"
            />
            <div class="d-flex">
              <v-text-field
                :value="parameter.minimum"
                type="number"
                :label="$t('workflowMinimum')"
                :disabled="disabled"
                clearable
                dense
                hide-details="auto"
                class="mr-1"
                @input="setInteger(index, 'minimum', $event)"
              />
              <v-text-field
                :value="parameter.maximum"
                type="number"
                :label="$t('workflowMaximum')"
                :disabled="disabled"
                clearable
                dense
                hide-details="auto"
                @input="setInteger(index, 'maximum', $event)"
              />
            </div>
          </template>

          <v-select
            v-else-if="parameter.type === 'boolean'"
            :value="parameter.default"
            :items="booleanOptions"
            item-value="value"
            item-text="text"
            :label="$t('default')"
            :disabled="disabled"
            clearable
            dense
            hide-details="auto"
            @change="setDefault(index, $event)"
          />

          <template v-else-if="parameter.type === 'enumeration'">
            <v-text-field
              :value="(parameter.options || []).join(', ')"
              :label="$t('workflowEnumerationOptions')"
              :disabled="disabled"
              dense
              hide-details="auto"
              @input="setOptions(index, $event)"
            />
            <v-select
              :value="parameter.default"
              :items="parameter.options || []"
              :label="$t('default')"
              :disabled="disabled"
              clearable
              dense
              hide-details="auto"
              @change="setDefault(index, $event)"
            />
          </template>

          <template v-else-if="parameter.type === 'secret_reference'">
            <v-autocomplete
              :value="secretOptionIds(parameter)"
              :items="credentials"
              item-value="id"
              item-text="name"
              :label="$t('workflowApprovedCredentials')"
              :disabled="disabled"
              multiple
              chips
              small-chips
              dense
              hide-details="auto"
              @change="setSecretOptions(index, $event)"
            />
            <v-select
              :value="parameter.default && parameter.default.access_key_id"
              :items="parameter.secret_options || []"
              item-value="access_key_id"
              item-text="label"
              :label="$t('default')"
              :disabled="disabled"
              clearable
              dense
              hide-details="auto"
              @change="setSecretDefault(index, $event)"
            />
          </template>

          <v-switch
            v-model="parameter.required"
            :label="$t('required')"
            :disabled="disabled"
            dense
            hide-details
            class="mt-1"
            @change="emitValue"
          />
        </v-card>

        <v-menu offset-y>
          <template v-slot:activator="{ on, attrs }">
            <v-btn
              text
              small
              color="primary"
              :disabled="disabled"
              v-bind="attrs"
              v-on="on"
            >
              <v-icon left small>mdi-plus</v-icon>
              {{ $t('workflowAddParameter') }}
            </v-btn>
          </template>
          <v-list dense>
            <v-list-item
              v-for="option in typeOptions"
              :key="option.value"
              @click="addParameter(option.value)"
            >
              <v-list-item-title>{{ option.text }}</v-list-item-title>
            </v-list-item>
          </v-list>
        </v-menu>
      </v-expansion-panel-content>
    </v-expansion-panel>
  </v-expansion-panels>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

export default {
  props: {
    value: { type: Array, default: () => [] },
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
    value: {
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
      );
    } catch (err) {
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
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
      this.$emit('input', JSON.parse(JSON.stringify(this.items)));
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
      this.$set(this.items, index, {
        ...this.newParameter(type),
        name: current.name,
        description: current.description,
        required: current.required,
      });
      this.emitValue();
    },
    setDefault(index, value) {
      if (value === null || value === undefined) this.$delete(this.items[index], 'default');
      else this.$set(this.items[index], 'default', value);
      this.emitValue();
    },
    setInteger(index, field, value) {
      if (value === null || value === undefined || value === '') {
        this.$delete(this.items[index], field);
      } else {
        this.$set(this.items[index], field, Number(value));
      }
      this.emitValue();
    },
    setOptions(index, value) {
      const options = value.split(',').map((entry) => entry.trim()).filter(Boolean);
      this.$set(this.items[index], 'options', options);
      if (this.items[index].default && !options.includes(this.items[index].default)) {
        this.$delete(this.items[index], 'default');
      }
      this.emitValue();
    },
    secretOptionIds(parameter) {
      return (parameter.secret_options || []).map((option) => option.access_key_id);
    },
    setSecretOptions(index, ids) {
      const options = ids.map((id) => {
        const key = this.credentials.find((entry) => entry.id === id);
        return { access_key_id: id, label: key ? key.name : `#${id}` };
      });
      this.$set(this.items[index], 'secret_options', options);
      const defaultId = this.items[index].default?.access_key_id;
      if (defaultId && !ids.includes(defaultId)) this.$delete(this.items[index], 'default');
      this.emitValue();
    },
    setSecretDefault(index, id) {
      this.setDefault(index, id == null ? null : { access_key_id: id });
    },
  },
};
</script>

<style scoped>
.WorkflowParameterEditor__content {
  margin: 0 -16px;
}
</style>
