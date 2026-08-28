const enhancedMethods = {
  syncEdge(edge) {
    const key = this.condKey(edge.source_node_id, edge.destination_node_id);
    this.edgeMetadata[key] = { ...this.edgeMetadata[key], ...edge };
    this.emitChange();
  },
  nextEdgeId() {
    const ids = Object.values(this.edgeMetadata)
      .map((metadata) => metadata.id)
      .filter((id) => id < 0);
    return ids.length === 0 ? -1 : Math.min(...ids) - 1;
  },
};

export default enhancedMethods;
