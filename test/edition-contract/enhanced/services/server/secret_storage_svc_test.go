package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
)

func TestStorageRequiresSecretFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		storage  db.SecretStorage
		required bool
	}{
		{name: "AWS IAM role", storage: db.SecretStorage{Type: db.SecretStorageTypeAwsSm, Params: db.MapStringAnyField{"use_iam_role": true}}},
		{name: "AWS explicit key", storage: db.SecretStorage{Type: db.SecretStorageTypeAwsSm, Params: db.MapStringAnyField{"use_iam_role": false}}, required: true},
		{name: "AWS missing mode", storage: db.SecretStorage{Type: db.SecretStorageTypeAwsSm}, required: true},
		{name: "AWS malformed mode", storage: db.SecretStorage{Type: db.SecretStorageTypeAwsSm, Params: db.MapStringAnyField{"use_iam_role": "true"}}, required: true},
		{name: "Vault token", storage: db.SecretStorage{Type: db.SecretStorageTypeVault, Params: db.MapStringAnyField{"auth_method": "token"}}, required: true},
		{name: "OpenBao Kubernetes auth", storage: db.SecretStorage{Type: db.SecretStorageTypeOpenBao, Params: db.MapStringAnyField{"auth_method": "kubernetes"}}, required: true},
		{name: "unknown provider", storage: db.SecretStorage{Type: db.SecretStorageType("unknown")}, required: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := StorageRequiresSecret(test.storage); actual != test.required {
				t.Fatalf("StorageRequiresSecret() = %v, want %v", actual, test.required)
			}
		})
	}
}
