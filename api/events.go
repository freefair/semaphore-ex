package api

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

// nolint: gocyclo
func getEvents(w http.ResponseWriter, r *http.Request, limit int) {
	user := helpers.GetFromContext(r, "user").(*db.User)
	projectObj, exists := helpers.GetOkFromContext(r, "project")

	params := db.RetrieveQueryParams{Count: limit, Viewer: &db.Viewer{UserID: user.ID, Admin: user.Admin}}
	var err error
	var events []db.Event

	if exists {
		// The route's read_project check already admitted the member.
		project := projectObj.(db.Project)
		events, err = helpers.Store(r).GetEvents(project.ID, params)
	} else {
		events, err = helpers.Store(r).GetUserEvents(user.ID, params)
	}

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, events)
}

func getLastEvents(w http.ResponseWriter, r *http.Request) {
	getEvents(w, r, 200)
}

func getAllEvents(w http.ResponseWriter, r *http.Request) {
	getEvents(w, r, 0)
}
