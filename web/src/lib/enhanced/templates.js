import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

const enhancedMethods = {
  normalizeTemplateSearch(value) {
    return typeof value === 'string' ? value.trim().slice(0, 256) : '';
  },
  cancelQueuedTemplateSearch() {
    if (this.templateSearchTimer != null) {
      clearTimeout(this.templateSearchTimer);
      this.templateSearchTimer = null;
    }
  },
  queueTemplateSearch() {
    this.cancelQueuedTemplateSearch();
    this.templateSearchTimer = setTimeout(() => {
      this.templateSearchTimer = null;
      this.applyTemplateSearch();
    }, 300);
  },
  async applyTemplateSearch() {
    this.cancelQueuedTemplateSearch();
    const normalized = this.normalizeTemplateSearch(this.templateSearchInput);
    this.templateSearchInput = normalized;
    this.appliedTemplateSearch = normalized;
    this.templateTablePage = 1;

    const query = { ...this.$route.query };
    delete query.page;
    if (normalized) {
      query.search = normalized;
    } else {
      delete query.search;
    }
    if (JSON.stringify(query) !== JSON.stringify(this.$route.query)) {
      await this.$router.push({ query });
    }
    await this.loadItems();
  },
  async clearTemplateSearch() {
    this.templateSearchInput = '';
    await this.applyTemplateSearch();
    await this.$nextTick();
    if (this.$refs.templateSearch) {
      this.$refs.templateSearch.focus();
    }
  },
  onTemplateSearchShortcut(event) {
    if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) {
      return;
    }
    const target = event.target;
    if (target && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))) {
      return;
    }
    event.preventDefault();
    if (this.$refs.templateSearch) {
      this.$refs.templateSearch.focus();
    }
  },
  cancelTemplateSearchRequest() {
    if (this.templateSearchAbort) {
      this.templateSearchAbort.abort();
      this.templateSearchAbort = null;
    }
  },
  async loadItems() {
    this.cancelTemplateSearchRequest();
    const controller = new AbortController();
    this.templateSearchRequest += 1;
    const request = this.templateSearchRequest;
    this.templateSearchAbort = controller;
    this.templateSearchLoading = true;
    this.templateSearchError = null;
    try {
      const response = await axios({
        method: 'get',
        url: this.getItemsUrl(),
        responseType: 'json',
        params: this.appliedTemplateSearch ? { search: this.appliedTemplateSearch } : undefined,
        signal: controller.signal,
      });
      if (request === this.templateSearchRequest) {
        this.items = response.data;
        this.openedItems = this.openedItems.filter((opened) => (
          this.items.some((item) => item.id === opened.id)
        ));
      }
    } catch (err) {
      if (!controller.signal.aborted && request === this.templateSearchRequest) {
        this.templateSearchError = getErrorMessage(err);
      }
    } finally {
      if (request === this.templateSearchRequest) {
        this.templateSearchAbort = null;
        this.templateSearchLoading = false;
      }
    }
  },
  highlightTemplateSearch(value) {
    const text = value == null ? '' : String(value);
    const query = this.appliedTemplateSearch.toLocaleLowerCase();
    if (!query) {
      return [{ text, match: false }];
    }
    const normalized = text.toLocaleLowerCase();
    const segments = [];
    let offset = 0;
    let matchIndex = normalized.indexOf(query, offset);
    while (matchIndex !== -1) {
      if (matchIndex > offset) {
        segments.push({ text: text.slice(offset, matchIndex), match: false });
      }
      const end = matchIndex + query.length;
      segments.push({ text: text.slice(matchIndex, end), match: true });
      offset = end;
      matchIndex = normalized.indexOf(query, offset);
    }
    if (offset < text.length || segments.length === 0) {
      segments.push({ text: text.slice(offset), match: false });
    }
    return segments;
  },
  templateSearchFields(item) {
    const tags = Array.isArray(item.runner_tags) && item.runner_tags.length > 0
      ? item.runner_tags.join(', ')
      : (item.runner_tag || '');
    return [
      { field: 'name', label: this.$t('name'), value: item.name || '' },
      { field: 'description', label: this.$t('description'), value: item.description || '' },
      { field: 'playbook', label: this.$t('playbook'), value: item.playbook || '' },
      { field: 'tags', label: this.$t('tags'), value: tags },
    ];
  },
  templateSearchSecondaryMatch(item) {
    if (!this.appliedTemplateSearch) {
      return null;
    }
    const query = this.appliedTemplateSearch.toLocaleLowerCase();
    return this.templateSearchFields(item).slice(1).find(
      (field) => field.value.toLocaleLowerCase().includes(query),
    ) || null;
  },
};

export default enhancedMethods;
