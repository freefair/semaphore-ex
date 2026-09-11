export function createEnhancedState() {
  return {
    templateSearchTimer: null,
    templateSearchAbort: null,
    templateSearchRequest: 0,
    templateSearchLoading: false,
    templateSearchError: null,
    templateTablePage: 1,
  };
}

export const enhancedWatch = {
  '$route.query.search': async function routeTemplateSearch(value) {
    const normalized = this.normalizeTemplateSearch(value);
    if (normalized === this.templateSearchInput
        && normalized === this.appliedTemplateSearch) {
      return;
    }
    this.cancelQueuedTemplateSearch();
    this.templateSearchInput = normalized;
    this.appliedTemplateSearch = normalized;
    this.templateTablePage = 1;
    await this.loadItems();
  },
};
