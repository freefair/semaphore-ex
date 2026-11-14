<template>
  <v-alert
    data-testid="test-alert-report"
    :type="report.sent ? 'success' : 'warning'"
    variant="outlined"
    density="compact"
    class="mt-4"
    role="status"
    aria-live="polite"
  >
    <div class="font-weight-medium mb-2">
      {{ report.sent ? $t('testAlertSentSummary') : $t('testAlertNotSentSummary') }}
    </div>
    <div
      v-for="channel in report.channels"
      :key="channel.channel"
      class="test-alert-row"
      :data-testid="`test-alert-${channel.channel}`"
    >
      <v-icon size="small" :color="statusColor(channel.status)">
        {{ statusIcon(channel.status) }}
      </v-icon>
      <span class="font-weight-medium">{{ channelName(channel.channel) }}</span>
      <span>{{ statusText(channel) }}</span>
      <span v-if="channel.status === 'not_configured'" class="test-alert-keys">
        {{ $t('testAlertConfigKeys') }}
        <code v-for="key in channel.config_keys" :key="key">{{ key }}</code>
      </span>
      <span v-if="channel.detail" class="test-alert-detail">{{ channel.detail }}</span>
    </div>
  </v-alert>
</template>

<script>
const CHANNEL_NAMES = {
  email: 'E-mail',
  telegram: 'Telegram',
  slack: 'Slack',
  rocketchat: 'Rocket.Chat',
  microsoft_teams: 'Microsoft Teams',
  dingtalk: 'DingTalk',
  gotify: 'Gotify',
};

export default {
  props: {
    report: { type: Object, required: true },
  },

  methods: {
    channelName(channel) {
      return CHANNEL_NAMES[channel] || channel;
    },

    statusText(channel) {
      switch (channel.status) {
        case 'sent': return this.$t('testAlertStatusSent');
        case 'failed': return this.$t('testAlertStatusFailed');
        case 'no_recipients': return this.$t('testAlertStatusNoRecipients');
        default: return this.$t('testAlertStatusNotConfigured');
      }
    },

    statusIcon(status) {
      if (status === 'sent') return 'mdi-check-circle';
      if (status === 'failed') return 'mdi-alert-circle';
      return 'mdi-minus-circle-outline';
    },

    statusColor(status) {
      if (status === 'sent') return 'success';
      if (status === 'failed') return 'error';
      return 'grey';
    },
  },
};
</script>

<style scoped>
.test-alert-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 8px;
  padding: 2px 0;
}
.test-alert-keys code {
  margin-left: 4px;
}
.test-alert-detail {
  flex-basis: 100%;
  padding-left: 28px;
  font-size: 0.875rem;
  opacity: 0.8;
  overflow-wrap: anywhere;
}
</style>
