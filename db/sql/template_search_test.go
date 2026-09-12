package sql

import (
	"testing"

	"github.com/Masterminds/squirrel"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateSearchMatchesLiteralTermsAcrossEverySearchField(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)

	description := "Description contains DeScRiPtIoN-NeeDle"
	legacyTag := "Legacy-TaG-NeEdLe"
	created := make(map[string]db.Template)
	for name, template := range map[string]db.Template{
		"name": {
			ProjectID: projectID, RepositoryID: repositoryID,
			Name: "NaMe-NeEdLe", Playbook: "site.yml",
		},
		"description": {
			ProjectID: projectID, RepositoryID: repositoryID,
			Name: "description", Playbook: "site.yml", Description: &description,
		},
		"playbook": {
			ProjectID: projectID, RepositoryID: repositoryID,
			Name: "playbook", Playbook: "PlAyBoOk-NeEdLe.yml",
		},
		"legacy-tag": {
			ProjectID: projectID, RepositoryID: repositoryID,
			Name: "legacy-tag", Playbook: "site.yml", RunnerTag: &legacyTag,
		},
		"tag-policy": {
			ProjectID: projectID, RepositoryID: repositoryID,
			Name: "tag-policy", Playbook: "site.yml", RunnerTags: db.StringArrayField{"secondary-TaG-NeEdLe"},
		},
		"literal": {
			ProjectID: projectID, RepositoryID: repositoryID,
			Name: "literal", Playbook: "literal-%_\\\x1f.yml",
		},
		"literal-sql": {
			ProjectID: projectID, RepositoryID: repositoryID,
			Name: "literal-sql", Playbook: "sql%_\\-literal.yml",
		},
		"literal-escape": {
			ProjectID: projectID, RepositoryID: repositoryID,
			Name: "literal-escape", Playbook: "bang!%_-literal.yml",
		},
	} {
		item, err := store.CreateTemplate(template)
		require.NoError(t, err)
		created[name] = item
	}

	for _, assertion := range []struct {
		term string
		key  string
	}{
		{term: "name-needle", key: "name"},
		{term: "  NAME-needle  ", key: "name"},
		{term: "description-needle", key: "description"},
		{term: "playbook-needle", key: "playbook"},
		{term: "legacy-tag-needle", key: "legacy-tag"},
		{term: "secondary-tag-needle", key: "tag-policy"},
		{term: "sql%_\\", key: "literal-sql"},
		{term: "bang!%_", key: "literal-escape"},
		{term: "%_\\\x1f", key: "literal"},
	} {
		t.Run(assertion.term, func(t *testing.T) {
			items, err := store.GetTemplates(projectID, db.TemplateFilter{Search: assertion.term}, db.RetrieveQueryParams{})
			require.NoError(t, err)
			require.Len(t, items, 1)
			assert.Equal(t, created[assertion.key].ID, items[0].ID)
		})
	}

	combined, err := store.GetTemplates(projectID, db.TemplateFilter{Search: "needle"}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, combined, 5, "one literal term must union matches across all searchable fields")
	assert.ElementsMatch(t, []int{
		created["name"].ID,
		created["description"].ID,
		created["playbook"].ID,
		created["legacy-tag"].ID,
		created["tag-policy"].ID,
	}, templateIDs(combined))

	emptySearch, err := store.GetTemplates(projectID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	for _, template := range emptySearch {
		if template.ID == created["tag-policy"].ID {
			assert.Equal(t, db.StringArrayField{"secondary-tag-needle"}, template.RunnerTags)
			return
		}
	}
	t.Fatal("ordinary template lists must retain runner tag context")
}

func TestTemplateSearchIsPermissionFirstAndPaginatesVisibleMatches(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)

	visible, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID,
		Name: "a-visible-needle", Playbook: "site.yml",
	})
	require.NoError(t, err)
	hidden, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID,
		Name: "b-hidden-needle", Playbook: "site.yml",
	})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-search-guest", Name: "Template Search Guest", Email: "template-search-guest@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: user.ID, Role: db.ProjectGuest})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: projectID, TemplateID: hidden.ID, RoleSlug: string(db.ProjectGuest),
		DeniedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)

	firstPage, err := store.GetTemplatesWithPermissions(projectID, user.ID,
		db.TemplateFilter{Search: "needle"}, db.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, firstPage, 1)
	assert.Equal(t, visible.ID, firstPage[0].ID)

	secondPage, err := store.GetTemplatesWithPermissions(projectID, user.ID,
		db.TemplateFilter{Search: "needle"}, db.RetrieveQueryParams{Count: 1, Offset: 1})
	require.NoError(t, err)
	assert.Empty(t, secondPage, "hidden matches must not create a visible second page")
}

func TestTemplateSearchKeepsStableIDTieBreakAndEmptyQueryOrdering(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)

	// Names are unique after 2.20.68; equal playbooks still require an ID tie-break.
	first, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repositoryID, Name: "same-name-b", Playbook: "site.yml"})
	require.NoError(t, err)
	second, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repositoryID, Name: "same-name-a", Playbook: "site.yml"})
	require.NoError(t, err)

	for _, filter := range []db.TemplateFilter{{}, {Search: "same-name"}} {
		items, err := store.GetTemplates(projectID, filter, db.RetrieveQueryParams{SortBy: "playbook"})
		require.NoError(t, err)
		require.Len(t, items, 2)
		assert.Equal(t, []int{first.ID, second.ID}, []int{items[0].ID, items[1].ID})
	}
	items, err := store.GetTemplates(projectID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, []int{second.ID, first.ID}, []int{items[0].ID, items[1].ID})
	items, err = store.GetTemplates(projectID, db.TemplateFilter{}, db.RetrieveQueryParams{SortBy: "playbook", Count: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, second.ID, items[0].ID)
}

func TestTemplateSearchRejectsMalformedAndUnboundedTerms(t *testing.T) {
	for _, search := range []string{
		string([]byte{0xff}),
		string(make([]rune, db.MaxTemplateSearchLength+1)),
	} {
		assert.Error(t, (db.TemplateFilter{Search: search}).ValidateSearch())
	}
}

func TestTemplateSearchDoesNotInjectTermsIntoAnySupportedSQLDialect(t *testing.T) {
	pattern := templateSearchSQLPattern("percent!%_\\literal")
	assert.Equal(t, "%percent!!!%!_\\literal%", pattern)
	condition := templateSearchSQLCondition(pattern, true)
	query, args, err := squirrel.Select("pt.id").From("project__template pt").Where(condition).ToSql()
	require.NoError(t, err)
	assert.Contains(t, query, "LOWER(pt.name) LIKE ? ESCAPE '!'")
	assert.Contains(t, query, "LOWER(pt.runner_tags) LIKE ? ESCAPE '!'")
	assert.Len(t, args, 5)
	for _, arg := range args {
		assert.Equal(t, pattern, arg)
	}

	// Every SQL engine receives the same parameterized literal pattern and the
	// standard ESCAPE clause. PostgreSQL additionally rewrites placeholders.
	d := SqlDb{}
	for _, dialect := range []gorp.Dialect{gorp.SqliteDialect{}, gorp.MySQLDialect{}, gorp.PostgresDialect{}} {
		prepared := d.connection.prepareQueryWithDialect(query, dialect)
		assert.NotContains(t, prepared, "percent")
		assert.Contains(t, prepared, "ESCAPE")
		assert.NotEmpty(t, prepared)
	}
}

func templateIDs(templates []db.Template) []int {
	ids := make([]int, 0, len(templates))
	for _, template := range templates {
		ids = append(ids, template.ID)
	}
	return ids
}
