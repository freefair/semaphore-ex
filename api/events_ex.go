package api

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

func getGlobalAuditEvents(w http.ResponseWriter, r *http.Request) {
	events, err := helpers.Store(r).GetAllEvents(db.RetrieveQueryParams{})
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, events)
}
