<template>
  <div class="admin-layout" data-testid="admin-layout">
    <nav class="admin-layout__nav" :aria-label="$t('administration')">
      <div class="admin-layout__heading">
        <v-btn
          icon
          variant="text"
          size="small"
          :aria-label="$t('adminBackToProjects')"
          data-testid="admin-back"
          @click="backToProjects"
        >
          <v-icon>mdi-arrow-left</v-icon>
        </v-btn>
        <span class="text-title-medium font-weight-medium">{{ $t('administration') }}</span>
      </div>

      <v-select
        class="admin-layout__select"
        :model-value="activeSection && activeSection.path"
        :items="sections"
        :item-title="(section) => $t(section.label)"
        item-value="path"
        :label="$t('administration')"
        variant="outlined"
        density="compact"
        hide-details
        data-testid="admin-section-select"
        @update:model-value="(path) => $router.push(path)"
      />

      <v-list class="admin-layout__list" density="compact" nav>
        <v-list-item
          v-for="section in sections"
          :key="section.key"
          :to="section.path"
          :active="activeSection && activeSection.key === section.key"
          :prepend-icon="section.icon"
          :title="$t(section.label)"
          :data-testid="`admin-nav-${section.key}`"
        />
      </v-list>
    </nav>

    <main class="admin-layout__content">
      <v-alert
        v-if="!sections.length"
        type="info"
        variant="outlined"
        data-testid="admin-no-access"
      >
        {{ $t('adminNoAccess') }}
      </v-alert>
      <router-view v-else v-bind="$attrs" :user="user" :system-info="systemInfo" />
    </main>
  </div>
</template>

<script>
import enhancedComputed from '@/lib/enhanced/app';
import { adminAccess, visibleAdminSections } from '@/lib/enhanced/admin-sections';

export default {
  inheritAttrs: false,

  // Admin pages also open outside this layout; inside it the section
  // navigation replaces their own back buttons.
  provide: { insideAdmin: true },

  props: {
    user: Object,
    systemInfo: Object,
  },

  computed: {
    ...enhancedComputed,

    sections() {
      return visibleAdminSections(adminAccess(this));
    },

    activeSection() {
      return this.sections.find((section) => this.$route.path.startsWith(section.path)) || null;
    },
  },

  watch: {
    sections: {
      immediate: true,
      handler() {
        this.openFirstSection();
      },
    },
    '$route.path': function routeChanged() {
      this.openFirstSection();
    },
  },

  methods: {
    openFirstSection() {
      if (this.$route.path.replace(/\/$/, '') === '/admin' && this.sections.length) {
        this.$router.replace(this.sections[0].path);
      }
    },

    backToProjects() {
      this.$router.push('/');
    },
  },
};
</script>

<style scoped>
.admin-layout {
  display: grid;
  grid-template-columns: 260px minmax(0, 1fr);
  min-height: 100%;
}
.admin-layout__nav {
  border-right: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 12px 8px;
}
.admin-layout__heading {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 4px 8px;
}
.admin-layout__select {
  display: none;
}
.admin-layout__content {
  min-width: 0;
}
@media (max-width: 900px) {
  .admin-layout {
    grid-template-columns: minmax(0, 1fr);
  }
  .admin-layout__nav {
    border-right: 0;
    border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  }
  .admin-layout__list {
    display: none;
  }
  .admin-layout__select {
    display: block;
  }
}
</style>
