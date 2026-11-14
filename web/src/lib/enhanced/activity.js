// Security audit records reach the project activity feed as JSON. Render them
// as a sentence; plain activity descriptions pass through unchanged.
function words(value) {
  return String(value || '').replace(/[._]+/g, ' ').trim();
}

export function parseAuditDescription(description) {
  if (typeof description !== 'string' || !description.trim().startsWith('{')) return null;
  try {
    const event = JSON.parse(description);
    return event && event.action && event.outcome ? event : null;
  } catch {
    return null;
  }
}

export function activityDescription(description, t) {
  const event = parseAuditDescription(description);
  if (!event) return description;
  const target = [words(event.target_type), event.target_id].filter(Boolean).join(' ');
  return t('activityAuditEvent', {
    action: words(event.action),
    target: target || '—',
    outcome: t(`activityOutcome_${event.outcome}`),
  });
}

// The people who appear in the loaded entries, for the user filter.
export function activityUsers(items) {
  return [...new Set((items || []).map((item) => item.username).filter(Boolean))]
    .sort((a, b) => a.localeCompare(b));
}

export function filterActivityByUser(items, username) {
  if (!username) return items || [];
  return (items || []).filter((item) => item.username === username);
}
