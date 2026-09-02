<template>
  <div>
    <v-toolbar flat>
      <v-app-bar-nav-icon @click="showDrawer" />
      <v-toolbar-title>{{ $t('keyStore') }}</v-toolbar-title>
    </v-toolbar>

    <KeyStoreMenu :project-id="projectId" />

    <v-alert v-if="!allowed" type="warning" text class="PageAlert" data-testid="granted-denied">
      Your project role does not allow listing granted credential metadata. Credential values are
      never available from this page.
    </v-alert>

    <template v-else>
      <v-alert type="info" text class="PageAlert">
        Select a granted reference by name. The stored value, owner, provider path, and fingerprint
        stay hidden. Runtime resolution and use are introduced separately.
      </v-alert>

      <v-data-table
        :headers="headers"
        :items="items"
        :loading="loading"
        hide-default-footer
        :items-per-page="100"
        data-testid="granted-credential-table"
      >
        <template v-slot:item.display_name="{ item }">
          <strong>{{ item.display_name }}</strong>
        </template>
        <template v-slot:item.version="{ item }">v{{ item.version }}</template>
        <template v-slot:item.operations="{ item }">{{ operationLabel(item.operations) }}</template>
        <template v-slot:item.expires_at="{ item }">{{ dateLabel(item.expires_at) }}</template>
        <template v-slot:item.select="{ item }">
          <v-radio-group v-model="selectedId" class="mt-0" hide-details>
            <v-radio :value="item.credential_id" :aria-label="`Select ${item.display_name}`" />
          </v-radio-group>
        </template>
      </v-data-table>

      <v-alert v-if="selected" type="success" text class="PageAlert" data-testid="granted-selected">
        Selected <strong>{{ selected.display_name }}</strong> as credential reference
        <code>{{ selected.credential_id }}</code>. No credential value was retrieved.
      </v-alert>
    </template>
  </div>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import KeyStoreMenu from '@/components/KeyStoreMenu.vue';
import { getErrorMessage } from '@/lib/error';
import { USER_PERMISSIONS } from '@/lib/constants';

export default {
  components: { KeyStoreMenu },

  props: {
    projectId: Number,
    userPermissions: Number,
    isAdmin: Boolean,
  },

  data() {
    return {
      loading: false,
      items: [],
      selectedId: null,
      headers: [
        { text: 'Name', value: 'display_name' },
        { text: 'Type', value: 'type' },
        { text: 'Version', value: 'version' },
        { text: 'Effective permission', value: 'operations' },
        { text: 'Expires', value: 'expires_at' },
        {
          text: 'Select', value: 'select', sortable: false, align: 'center',
        },
      ],
    };
  },

  computed: {
    allowed() {
      if (this.isAdmin) return true;
      // eslint-disable-next-line no-bitwise
      return ((this.userPermissions || 0) & USER_PERMISSIONS.listGrantedCredentials)
        === USER_PERMISSIONS.listGrantedCredentials;
    },
    selected() {
      return this.items.find((item) => item.credential_id === this.selectedId) || null;
    },
  },

  created() {
    if (this.allowed) this.load();
  },

  methods: {
    showDrawer() {
      EventBus.$emit('i-show-drawer');
    },
    async load() {
      if (!this.allowed) return;
      this.loading = true;
      try {
        this.items = (
          await axios.get(`/api/project/${this.projectId}/granted-credentials?count=100&offset=0`)
        ).data || [];
        if (!this.items.some((item) => item.credential_id === this.selectedId)) {
          this.selectedId = null;
        }
      } catch (error) {
        this.notifyError(error);
      } finally {
        this.loading = false;
      }
    },
    operationLabel(operations) {
      if (operations === 3) return 'Reference and consume';
      if (operations === 2) return 'Consume only';
      return 'Reference only';
    },
    dateLabel(value) {
      return value ? new Date(value).toLocaleString() : 'Never';
    },
    notifyError(error) {
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(error) });
    },
  },
};
</script>
