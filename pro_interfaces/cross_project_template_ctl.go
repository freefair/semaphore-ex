package pro_interfaces

import "net/http"

type CrossProjectTemplateController interface {
	PublishTemplateVersion(http.ResponseWriter, *http.Request)
	ListTemplateVersions(http.ResponseWriter, *http.Request)
	CreateGrant(http.ResponseWriter, *http.Request)
	ListGrants(http.ResponseWriter, *http.Request)
	UpdateGrant(http.ResponseWriter, *http.Request)
	DeleteGrant(http.ResponseWriter, *http.Request)
	AcceptGrant(http.ResponseWriter, *http.Request)
	RevokeGrant(http.ResponseWriter, *http.Request)
	ListReferences(http.ResponseWriter, *http.Request)
}

type CrossProjectTemplateAuditConfigurer interface {
	ConfigureCrossProjectTemplateAudit(AuditServiceFacade)
}
