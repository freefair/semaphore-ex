export default {
  artifactAvailabilityColor(availability) {
    if (availability === 'available') return 'success';
    if (availability === 'invalid') return 'error';
    return 'grey';
  },
  fileArtifactDownloadable(artifact) {
    return artifact.state === 'available'
        && (!artifact.expires_at || new Date(artifact.expires_at).getTime() > Date.now());
  },
  fileArtifactStateLabel(artifact) {
    return this.fileArtifactDownloadable(artifact)
      ? this.$t('workflowFileArtifactAvailable')
      : this.$t('workflowFileArtifactExpired');
  },
  fileArtifactStateColor(artifact) {
    if (!this.fileArtifactDownloadable(artifact)) return 'grey';
    const expiresAt = artifact.expires_at ? new Date(artifact.expires_at).getTime() : 0;
    return expiresAt && expiresAt - Date.now() <= 24 * 60 * 60 * 1000
      ? 'warning' : 'success';
  },
  fileArtifactExpiryClass(artifact) {
    return this.fileArtifactDownloadable(artifact) ? 'text--secondary' : 'error--text';
  },
  fileArtifactExpiryLabel(artifact) {
    if (!this.fileArtifactDownloadable(artifact)) return this.$t('workflowFileArtifactNoLongerAvailable');
    return this.$t('workflowFileArtifactExpires', { value: this.formatDate(artifact.expires_at) });
  },
  formatBytes(value) {
    const bytes = Number(value) || 0;
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
  },
  schemaSummary(schema) {
    return JSON.stringify(schema || {});
  },
  nodeLabel(nodeId) {
    const node = (this.workflow?.nodes || []).find((entry) => entry.id === nodeId);
    return node?.display_name ? `#${nodeId} ${node.display_name}` : `#${nodeId}`;
  },
  formatDate(value) {
    return value ? new Date(value).toLocaleString() : '';
  },
};
