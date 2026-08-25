export const COMMUNITY_EDITION = 'community';
export const ENHANCED_EDITION = 'enhanced';

export function resolveEdition(serverEdition, buildEdition = process.env.VUE_APP_EDITION) {
  if (serverEdition === ENHANCED_EDITION || serverEdition === COMMUNITY_EDITION) {
    return serverEdition;
  }
  return buildEdition === ENHANCED_EDITION ? ENHANCED_EDITION : COMMUNITY_EDITION;
}

export function isEnhancedEdition(serverEdition, buildEdition) {
  return resolveEdition(serverEdition, buildEdition) === ENHANCED_EDITION;
}
