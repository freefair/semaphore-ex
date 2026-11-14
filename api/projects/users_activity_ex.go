package projects

import (
	"fmt"
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
)

// teamUserLabel names a team member in the project activity feed by the name
// people recognise instead of an internal user ID.
func teamUserLabel(user db.User) string {
	switch {
	case user.Name != "" && user.Username != "":
		return fmt.Sprintf("%s (%s)", user.Name, user.Username)
	case user.Username != "":
		return user.Username
	case user.Name != "":
		return user.Name
	default:
		return fmt.Sprintf("User #%d", user.ID)
	}
}

func teamMemberLabel(r *http.Request, userID int) string {
	user, err := helpers.Store(r).GetUser(userID)
	if err != nil {
		return fmt.Sprintf("User #%d", userID)
	}
	return teamUserLabel(user)
}
