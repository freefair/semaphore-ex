const enhancedComputed = {
  runnerIdentity() {
    const id = this.item?.used_runner_id;
    const name = this.item?.used_runner_name;
    if (id == null) return name || '';
    return name ? `#${id} — ${name}` : `#${id}`;
  },
};

export default enhancedComputed;
