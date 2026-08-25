import enhancedMessages from './enhanced';

const files = require.context('.', false, /\.js$/);
const messages = {};
files.keys().forEach((key) => {
  if (key === './index.js') return;
  const locale = key.replace(/(\.\/|\.js)/g, '');
  messages[locale] = { ...files(key).default, ...(enhancedMessages[locale] || {}) };
});
const languages = Object.keys(messages);
export { messages, languages };
