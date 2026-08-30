// Node 25 exposes an incomplete localStorage object unless a backing file is configured.
// Load the in-memory fixture before component specs import the application i18n plugin.
import './local-storage-fixture';
