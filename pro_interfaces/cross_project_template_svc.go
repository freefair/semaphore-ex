package pro_interfaces

import "github.com/semaphoreui/semaphore/db"

// CrossProjectTemplateReferenceView is deliberately narrower than
// db.TemplateVersion. It is the only representation exposed to consumers.
type CrossProjectTemplateReferenceView struct {
	GrantID               int             `json:"grant_id"`
	GrantRevision         int             `json:"grant_revision"`
	OwnerProjectID        int             `json:"owner_project_id"`
	TemplateID            int             `json:"template_id"`
	TemplateVersionID     int             `json:"template_version_id"`
	TemplateVersionNumber int             `json:"template_version_number"`
	ContentFingerprint    string          `json:"content_fingerprint"`
	Name                  string          `json:"name"`
	Type                  db.TemplateType `json:"type,omitempty"`
}

type CrossProjectTemplateGrantCreate struct {
	ConsumerProjectID int                                   `json:"consumer_project_id"`
	MinVersion        int                                   `json:"min_version"`
	MaxVersion        int                                   `json:"max_version"`
	Operations        db.CrossProjectTemplateGrantOperation `json:"operations"`
	Reason            string                                `json:"reason"`
}

type CrossProjectTemplateGrantUpdate struct {
	MinVersion       int                                   `json:"min_version"`
	MaxVersion       int                                   `json:"max_version"`
	Operations       db.CrossProjectTemplateGrantOperation `json:"operations"`
	Reason           string                                `json:"reason"`
	ExpectedRevision int                                   `json:"expected_revision"`
}

// CrossProjectTemplateService is an Enhanced-only facade. It owns all dual
// project authorization and keeps controllers away from grant persistence.
type CrossProjectTemplateService interface {
	PublishTemplateVersion(projectID, templateID int, actor *db.User) (db.TemplateVersion, bool, error)
	ListTemplateVersions(projectID, templateID int, params db.RetrieveQueryParams, actor *db.User) ([]db.TemplateVersion, error)
	CreateGrant(ownerProjectID, templateID int, command CrossProjectTemplateGrantCreate, actor *db.User) (db.CrossProjectTemplateGrant, error)
	UpdateGrant(ownerProjectID, grantID int, command CrossProjectTemplateGrantUpdate, actor *db.User) (db.CrossProjectTemplateGrant, error)
	DeleteGrant(ownerProjectID, grantID, expectedRevision int, actor *db.User) (db.CrossProjectTemplateGrant, error)
	AcceptGrant(consumerProjectID, grantID, expectedRevision int, actor *db.User) (db.CrossProjectTemplateGrant, error)
	RevokeGrant(projectID, grantID, expectedRevision int, reason string, actor *db.User) (db.CrossProjectTemplateGrant, error)
	ListGrants(projectID int, params db.RetrieveQueryParams, actor *db.User) ([]db.CrossProjectTemplateGrant, error)
	ListReferences(consumerProjectID int, grantID int, params db.RetrieveQueryParams, actor *db.User) ([]CrossProjectTemplateReferenceView, db.CrossProjectTemplateGrant, error)
}
