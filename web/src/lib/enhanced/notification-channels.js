// Notification channels are configured in the server configuration, not in
// the UI. These keys match util.ConfigType and the test alert report; the
// environment variable of each key is SEMAPHORE_ followed by the key in
// upper case.
export const NOTIFICATION_CHANNELS = [
  { key: 'email', name: 'E-mail', configKeys: ['email_alert', 'email_sender', 'email_host', 'email_port'] },
  { key: 'telegram', name: 'Telegram', configKeys: ['telegram_alert', 'telegram_chat', 'telegram_token'] },
  { key: 'slack', name: 'Slack', configKeys: ['slack_alert', 'slack_url'] },
  { key: 'rocketchat', name: 'Rocket.Chat', configKeys: ['rocketchat_alert', 'rocketchat_url'] },
  { key: 'microsoft_teams', name: 'Microsoft Teams', configKeys: ['microsoft_teams_alert', 'microsoft_teams_url'] },
  { key: 'dingtalk', name: 'DingTalk', configKeys: ['dingtalk_alert', 'dingtalk_url'] },
  { key: 'gotify', name: 'Gotify', configKeys: ['gotify_alert', 'gotify_url', 'gotify_token'] },
];

export function environmentVariable(configKey) {
  return `SEMAPHORE_${configKey.toUpperCase()}`;
}

export function notificationChannelStates(notifications) {
  return NOTIFICATION_CHANNELS.map((channel) => ({
    ...channel,
    configured: Boolean((notifications || {})[channel.key]),
  }));
}
