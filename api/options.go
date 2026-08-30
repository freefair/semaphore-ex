package api

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
)

func setOption(w http.ResponseWriter, r *http.Request) {
	currentUser := helpers.GetFromContext(r, "user").(*db.User)

	allowed, err := hasGlobalPermission(r, currentUser, db.CanManageGlobalSystem)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if !allowed {
		helpers.WriteJSON(w, http.StatusForbidden, map[string]string{
			"error": "User must have global system management permission",
		})
		return
	}

	var option db.Option
	if !helpers.Bind(w, r, &option) {
		return
	}

	err = helpers.Store(r).SetOption(option.Key, option.Value)
	if err != nil {
		helpers.WriteJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Can not set option",
		})
		return
	}

	helpers.WriteJSON(w, http.StatusOK, option)
}

func getOptions(w http.ResponseWriter, r *http.Request) {
	currentUser := helpers.GetFromContext(r, "user").(*db.User)

	allowed, err := hasGlobalPermission(r, currentUser, db.CanManageGlobalSystem)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if !allowed {
		helpers.WriteJSON(w, http.StatusForbidden, map[string]string{
			"error": "User must have global system management permission",
		})
		return
	}

	options, err := helpers.Store(r).GetOptions(db.RetrieveQueryParams{})
	if err != nil {
		helpers.WriteJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Can not get options",
		})
		return
	}

	helpers.WriteJSON(w, http.StatusOK, options)
}
