package infisicalpam_test

import (
	"context"
	"testing"

	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSecret(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{Secrets: map[string]pamtest.FakeSecret{
		pamtest.SecretKey("proj-1", "prod", "/", "LICENSE_KEY"):         {Value: "license-value", Version: 3},
		pamtest.SecretKey("proj-1", "prod", "/apps/web", "DB_PASSWORD"): {Value: "db-value", Version: 5},
		pamtest.SecretKey("proj-1", "prod", "/", "HIDDEN"):              {Value: "never", Version: 1, Hidden: true},
	}})
	client := newTestClient(t, env, pamtest.ClientSecret)
	token, err := client.Login(context.Background())
	require.NoError(t, err)

	t.Run("root folder", func(t *testing.T) {
		secret, err := client.GetSecret(context.Background(), token, infisicalpam.SecretRequest{
			ProjectID: "proj-1", Environment: "prod", Path: "/", Name: "LICENSE_KEY",
		})
		require.NoError(t, err)
		assert.Equal(t, "license-value", string(secret.Value))
		assert.Equal(t, 3, secret.Version)
	})

	t.Run("nested folder and pinned version", func(t *testing.T) {
		secret, err := client.GetSecret(context.Background(), token, infisicalpam.SecretRequest{
			ProjectID: "proj-1", Environment: "prod", Path: "/apps/web", Name: "DB_PASSWORD", Version: 5,
		})
		require.NoError(t, err)
		assert.Equal(t, "db-value", string(secret.Value))
	})

	t.Run("failures are categorized", func(t *testing.T) {
		tests := []struct {
			name     string
			token    infisicalpam.AccessToken
			request  infisicalpam.SecretRequest
			category infisicalpam.ErrorCategory
		}{
			{"unknown secret", token, infisicalpam.SecretRequest{ProjectID: "proj-1", Environment: "prod", Path: "/", Name: "MISSING"}, infisicalpam.ErrorNotFound},
			{"other version", token, infisicalpam.SecretRequest{ProjectID: "proj-1", Environment: "prod", Path: "/", Name: "LICENSE_KEY", Version: 9}, infisicalpam.ErrorNotFound},
			{"value hidden from the identity", token, infisicalpam.SecretRequest{ProjectID: "proj-1", Environment: "prod", Path: "/", Name: "HIDDEN"}, infisicalpam.ErrorPermission},
			{"invalid token", infisicalpam.AccessToken{Value: "wrong"}, infisicalpam.SecretRequest{ProjectID: "proj-1", Environment: "prod", Path: "/", Name: "LICENSE_KEY"}, infisicalpam.ErrorAuthentication},
			{"relative path", token, infisicalpam.SecretRequest{ProjectID: "proj-1", Environment: "prod", Path: "apps", Name: "LICENSE_KEY"}, infisicalpam.ErrorValidation},
			{"path traversal", token, infisicalpam.SecretRequest{ProjectID: "proj-1", Environment: "prod", Path: "/apps/..", Name: "LICENSE_KEY"}, infisicalpam.ErrorValidation},
			{"name with a slash", token, infisicalpam.SecretRequest{ProjectID: "proj-1", Environment: "prod", Path: "/", Name: "a/b"}, infisicalpam.ErrorValidation},
			{"environment with uppercase", token, infisicalpam.SecretRequest{ProjectID: "proj-1", Environment: "Prod", Path: "/", Name: "LICENSE_KEY"}, infisicalpam.ErrorValidation},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := client.GetSecret(context.Background(), tt.token, tt.request)
				require.Error(t, err)
				assert.Equal(t, tt.category, infisicalpam.CategoryOf(err))
				assert.NotContains(t, err.Error(), "never")
			})
		}
	})
}
