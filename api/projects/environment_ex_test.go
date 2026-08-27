package projects

import (
	"github.com/semaphoreui/semaphore/db"
	"testing"
)

func TestUpdateEnvironmentRejectsInvalidRuntimeReference(t *testing.T) {
	storageID := 9
	svc := &mockAccessKeyService{}
	controller := &EnvironmentController{accessKeyService: svc}
	environment := db.Environment{ID: 4, ProjectID: 3}
	environment.Secrets = append(environment.Secrets, db.EnvironmentSecret{
		Type: db.EnvironmentSecretEnv, Name: "TOKEN", Operation: db.EnvironmentSecretCreate,
		StorageID: &storageID, Mount: "team", Path: "../escape", Field: "value",
	})

	err := controller.updateEnvironmentSecrets(environment)

	if err == nil {
		t.Fatal("expected invalid runtime reference to be rejected")
	}
	if len(svc.created) != 0 {
		t.Fatalf("expected no access key creation, got %d", len(svc.created))
	}
}
