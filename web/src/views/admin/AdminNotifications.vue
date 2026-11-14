<template>
  <div data-testid="admin-notifications">
    <v-toolbar flat>
      <v-toolbar-title>{{ $t('adminNotifications') }}</v-toolbar-title>
    </v-toolbar>

    <div class="px-4 pb-6">
      <v-alert v-if="error" type="error" variant="outlined" density="compact" class="mb-4">
        {{ error }}
      </v-alert>

      <p class="mb-4">{{ $t('adminNotificationsServerConfig') }}</p>

      <v-progress-linear v-if="loading" indeterminate class="mb-4" />

      <v-table v-else density="comfortable" data-testid="admin-notification-channels">
        <thead>
          <tr>
            <th>{{ $t('adminNotificationChannel') }}</th>
            <th>{{ $t('adminNotificationState') }}</th>
            <th>{{ $t('adminNotificationKeys') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="channel in channels"
            :key="channel.key"
            :data-testid="`admin-notification-${channel.key}`"
          >
            <td class="font-weight-medium">{{ channel.name }}</td>
            <td>
              <v-chip
                size="small"
                :color="channel.configured ? 'success' : undefined"
                :prepend-icon="channel.configured ? 'mdi-check-circle' : 'mdi-minus-circle-outline'"
              >
                {{ channel.configured
                  ? $t('adminNotificationConfigured') : $t('adminNotificationNotConfigured') }}
              </v-chip>
            </td>
            <td>
              <div v-for="key in channel.configKeys" :key="key" class="admin-notification-key">
                <code>{{ key }}</code>
                <span class="text-medium-emphasis">{{ environmentVariable(key) }}</span>
              </div>
            </td>
          </tr>
        </tbody>
      </v-table>

      <h3 class="text-title-small font-weight-medium mt-6 mb-2">
        {{ $t('adminNotificationsRecipientsTitle') }}
      </h3>
      <p>{{ $t('adminNotificationsRecipients') }}</p>
    </div>
  </div>
</template>

<script>
import axios from 'axios';
import { environmentVariable, notificationChannelStates } from '@/lib/enhanced/notification-channels';

export default {
  data() {
    return {
      notifications: null,
      loading: false,
      error: null,
    };
  },

  computed: {
    channels() {
      return notificationChannelStates(this.notifications);
    },
  },

  async created() {
    this.loading = true;
    try {
      this.notifications = (await axios.get('/api/admin/info')).data.notifications || {};
    } catch (err) {
      this.error = err.response?.data?.message || err.message;
    } finally {
      this.loading = false;
    }
  },

  methods: {
    environmentVariable,
  },
};
</script>

<style scoped>
.admin-notification-key {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: baseline;
  padding: 2px 0;
}
</style>
