// Keeps the first item for every id, in order.
export default function uniqueById(items) {
  const seen = new Set();
  return (items || []).filter((item) => {
    if (seen.has(item.id)) return false;
    seen.add(item.id);
    return true;
  });
}
