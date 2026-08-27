package projects

import (
	"net/http"
)

func (c *ProjectRunnerControllerImpl) GetRunnerHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *ProjectRunnerControllerImpl) GetRunnerHistory(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
