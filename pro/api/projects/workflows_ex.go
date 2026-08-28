package projects

import (
	"net/http"
)

func (c *workflowController) ValidateWorkflow(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
