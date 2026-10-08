// Match the server's repository-relative directory spelling without changing
// the authored value which the template saves.
export function normalizeRepositoryPath(value) {
  const parts = [];
  String(value).split('/').forEach((part) => {
    if (part === '..') {
      parts.pop();
    } else if (part && part !== '.') {
      parts.push(part);
    }
  });
  return parts.join('/');
}

export function filterRepositoryPath(item, query, text) {
  const search = this.fields.playbook?.directories ? normalizeRepositoryPath(query) : query;
  return String(text).toLocaleLowerCase().includes(String(search).toLocaleLowerCase());
}
