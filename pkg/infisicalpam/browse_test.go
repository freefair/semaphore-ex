package infisicalpam

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func browseFixture(t *testing.T) (*Client, AccessToken, *pamtest.Environment) {
	t.Helper()
	env := pamtest.Start(t, pamtest.Options{
		Organization: pamtest.FakeOrganization{ID: "org-1", Name: "Acme", Slug: "acme"},
		Projects: []pamtest.FakeProject{
			{ID: "p-2", Name: "licenses", Slug: "licenses", Environments: []pamtest.FakeEnvironment{{Name: "Production", Slug: "prod"}}},
			{ID: "p-1", Name: "Infrastructure", Slug: "infrastructure", Environments: []pamtest.FakeEnvironment{{Name: "Development", Slug: "dev"}, {Name: "Production", Slug: "prod"}}},
		},
		Secrets: map[string]pamtest.FakeSecret{
			pamtest.SecretKey("p-1", "prod", "/", "ROOT_TOKEN"):             {Value: "root-value", Version: 1},
			pamtest.SecretKey("p-1", "prod", "/apps/web", "DB_PASSWORD"):    {Value: "web-value", Version: 4},
			pamtest.SecretKey("p-1", "prod", "/apps/billing", "STRIPE_KEY"): {Value: "billing-value", Version: 2},
			pamtest.SecretKey("p-1", "dev", "/apps/web", "DB_PASSWORD"):     {Value: "dev-value", Version: 1},
			pamtest.SecretKey("p-2", "prod", "/", "LICENSE_KEY"):            {Value: "license-value", Version: 7},
		},
	})
	client, err := NewClient(Config{SiteURL: env.API.URL, ClientID: pamtest.ClientID, ClientSecret: []byte(pamtest.ClientSecret), CACertificate: env.APICACert})
	require.NoError(t, err)
	token, err := client.Login(context.Background())
	require.NoError(t, err)
	return client, token, env
}

func TestBrowseReturnsNamesWithoutValues(t *testing.T) {
	client, token, env := browseFixture(t)
	ctx := context.Background()

	t.Run("the organization proves the connection", func(t *testing.T) {
		organization, err := client.Organization(ctx, token)
		require.NoError(t, err)
		assert.Equal(t, Organization{ID: "org-1", Name: "Acme", Slug: "acme"}, organization)
	})

	t.Run("projects come with names and environments, sorted by name", func(t *testing.T) {
		projects, err := client.Projects(ctx, token)
		require.NoError(t, err)
		require.Len(t, projects, 2)
		assert.Equal(t, "Infrastructure", projects[0].Name)
		assert.Equal(t, []Environment{{Name: "Development", Slug: "dev"}, {Name: "Production", Slug: "prod"}}, projects[0].Environments)
		assert.Equal(t, "licenses", projects[1].Name)
	})

	t.Run("folders are listed one level at a time", func(t *testing.T) {
		root, err := client.Folders(ctx, token, FolderRequest{ProjectID: "p-1", Environment: "prod", Path: "/"})
		require.NoError(t, err)
		assert.Equal(t, []Folder{{Name: "apps"}}, root)
		apps, err := client.Folders(ctx, token, FolderRequest{ProjectID: "p-1", Environment: "prod", Path: "/apps"})
		require.NoError(t, err)
		assert.Equal(t, []Folder{{Name: "billing"}, {Name: "web"}}, apps)
	})

	t.Run("secret keys never carry values", func(t *testing.T) {
		keys, err := client.SecretKeys(ctx, token, FolderRequest{ProjectID: "p-1", Environment: "prod", Path: "/apps/web"})
		require.NoError(t, err)
		assert.Equal(t, []SecretKey{{Key: "DB_PASSWORD", Version: 4}}, keys)
		encoded, err := json.Marshal(keys)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "web-value")

		var listing string
		for _, request := range env.Browsed() {
			if strings.HasPrefix(request, "/api/v4/secrets?") {
				listing = request
			}
		}
		assert.Contains(t, listing, "viewSecretValue=false", "the client must ask Infisical not to send values")
	})

	t.Run("invalid locations are refused before any request", func(t *testing.T) {
		before := len(env.Browsed())
		for _, request := range []FolderRequest{
			{ProjectID: "p-1", Environment: "prod", Path: "apps"},
			{ProjectID: "p-1", Environment: "prod", Path: "/apps/../etc"},
			{ProjectID: "p 1", Environment: "prod", Path: "/"},
			{ProjectID: "p-1", Environment: "Prod!", Path: "/"},
		} {
			_, err := client.Folders(ctx, token, request)
			assert.Equal(t, ErrorValidation, CategoryOf(err), request.Path)
		}
		assert.Len(t, env.Browsed(), before)
	})

	t.Run("an invalid token is an authentication error", func(t *testing.T) {
		_, err := client.Projects(ctx, AccessToken{Value: "wrong"})
		assert.Equal(t, ErrorAuthentication, CategoryOf(err))
	})
}
