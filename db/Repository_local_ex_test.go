package db

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestRepositoryLocalPathRejectsSharedCheckout(t *testing.T) {
	for _, url := range []string{"/srv/repository", `C:\repository`, `\\server\share`} {
		t.Run(url, func(t *testing.T) {
			repo := Repository{Name: "Local", GitURL: url, Monorepo: true}
			assert.ErrorContains(t, repo.Validate(), "shared checkout")
			repo.Monorepo = false
			assert.NoError(t, repo.Validate())
		})
	}
	for _, url := range []string{"https://example.com/repository.git", "file:///srv/repository"} {
		t.Run(url, func(t *testing.T) {
			assert.NoError(t, (Repository{Name: "Git", GitURL: url, GitBranch: "main", Monorepo: true}).Validate())
		})
	}
}
