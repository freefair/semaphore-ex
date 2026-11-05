import enhancedMessages from './enhanced';

const files = import.meta.glob('./*.js', { eager: true });
const messages = {};
Object.keys(files).forEach((key) => {
  if (key === './index.js') return;
  const locale = key.slice(2, -3);
  messages[locale] = { ...files[key].default, ...(enhancedMessages[locale] || {}) };
});
const languages = Object.keys(messages);
export { messages, languages };
