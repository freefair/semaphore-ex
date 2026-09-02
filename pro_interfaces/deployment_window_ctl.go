package pro_interfaces

import "net/http"

// DeploymentWindowController is the replaceable HTTP boundary for project
// deployment-window governance. Community keeps these routes stable but
// unavailable; Enhanced supplies the authorized implementation.
type DeploymentWindowController interface {
	GetPolicy(http.ResponseWriter, *http.Request)
	SavePolicy(http.ResponseWriter, *http.Request)
	ResetPolicy(http.ResponseWriter, *http.Request)
	Preview(http.ResponseWriter, *http.Request)
	CurrentStatus(http.ResponseWriter, *http.Request)
	DecisionHistory(http.ResponseWriter, *http.Request)
}
