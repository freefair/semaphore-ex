/**
 * Classifies repository URL/path the same way the Semaphore API expects:
 * Unix absolute (/...), Windows drive (D:/...), UNC (\\server\...), or Git URL.
 *
 * @param {string|null|undefined} url
 * @returns {'local'|'ssh'|'git'|'http'|'https'|'file'|null}
 */
export function getRepositoryUrlType(url) {
  if (url == null || url === '') {
    return null;
  }

  if (url.startsWith('/')) {
    return 'local';
  }

  // Windows UNC
  if (/^\\\\./.test(url)) {
    return 'local';
  }

  // Match the backend's drive-path boundary; c:org/repo is an SSH host alias.
  if (/^[a-zA-Z]:(?:$|\\|\/(?!\/))/.test(url)) {
    return 'local';
  }

  const m = url.match(/^(\w+):\/\//);

  if (m == null) {
    return 'ssh';
  }

  if (!['git', 'file', 'ssh', 'http', 'https'].includes(m[1])) {
    return null;
  }

  return m[1];
}

export function isLocalRepositoryPath(url) {
  return getRepositoryUrlType(url) === 'local';
}
