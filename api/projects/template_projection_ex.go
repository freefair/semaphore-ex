package projects

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
)

// templateResponse adds derived execution metadata to an otherwise unchanged
// template response. It deliberately exposes no repository data: callers that
// can read a template need only know whether branch overrides are valid.
type templateResponse struct {
	db.Template
	Permissions        *db.ProjectUserPermission `json:"permissions"`
	RepositoryMonorepo bool                      `json:"repository_monorepo"`
}

func projectTemplateResponse(
	template db.TemplateWithPerms,
	repositoryMonorepo bool,
) templateResponse {
	return templateResponse{
		Template:           template.Template,
		Permissions:        template.Permissions,
		RepositoryMonorepo: repositoryMonorepo,
	}
}

func projectTemplateResponseFor(
	store db.RepositoryManager,
	projectID int,
	template db.TemplateWithPerms,
) (templateResponse, error) {
	repository, err := store.GetRepository(projectID, template.RepositoryID)
	if errors.Is(err, db.ErrNotFound) {
		// A legacy dangling template keeps its historical response shape plus an
		// explicit false flag, rather than revealing repository state or failing
		// an otherwise authorized template read.
		return projectTemplateResponse(template, false), nil
	}
	if err != nil {
		return templateResponse{}, err
	}
	return projectTemplateResponse(template, repository.Monorepo), nil
}

func projectTemplateResponsesFor(
	store db.RepositoryManager,
	projectID int,
	templates []db.TemplateWithPerms,
) ([]templateResponse, error) {
	repositories, err := store.GetRepositories(projectID, db.RetrieveQueryParams{})
	if err != nil {
		return nil, err
	}

	monorepoByRepositoryID := make(map[int]bool, len(repositories))
	for _, repository := range repositories {
		monorepoByRepositoryID[repository.ID] = repository.Monorepo
	}

	responses := make([]templateResponse, 0, len(templates))
	for _, template := range templates {
		responses = append(responses, projectTemplateResponse(
			template,
			monorepoByRepositoryID[template.RepositoryID],
		))
	}
	return responses, nil
}
