package api

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

type SystemInfoController struct {
}

type SystemInfo struct {
	Version           string                                    `json:"version"`
	Ansible           string                                    `json:"ansible"`
	WebHost           string                                    `json:"web_host"`
	UseRemoteRunner   bool                                      `json:"use_remote_runner"`
	AuthMethods       LoginAuthMethods                          `json:"auth_methods"`
	LoginWithPassword bool                                      `json:"login_with_password"`
	Features          pro_interfaces.Features                   `json:"features"`
	SubscriptionState string                                    `json:"subscription_state"`
	GitClient         string                                    `json:"git_client"`
	ScheduleTimezone  string                                    `json:"schedule_timezone"`
	Teams             *util.TeamsConfig                         `json:"teams"`
	Roles             []db.Role                                 `json:"roles"`
	BoltdbUsed        bool                                      `json:"boltdb_used"`
	JWT               SystemInfoJWT                             `json:"jwt"`
	Edition           pro_interfaces.Edition                    `json:"edition"`
	ContractVersion   string                                    `json:"contract_version"`
	Implementation    string                                    `json:"implementation_version"`
	CoreRevision      string                                    `json:"core_revision"`
	EnhancedRevision  string                                    `json:"enhanced_revision,omitempty"`
	Capabilities      pro_interfaces.CapabilitySnapshot         `json:"capabilities"`
	GlobalPermissions pro_interfaces.EffectiveGlobalPermissions `json:"global_permissions"`
}

// SystemInfoJWT exposes the global JWT configuration for the WebUI.
type SystemInfoJWT struct {
	Enabled bool   `json:"enabled"`
	MaxTTL  string `json:"max_ttl,omitempty"`
}

func NewSystemInfoController() *SystemInfoController {
	return &SystemInfoController{}
}

func (c *SystemInfoController) GetSystemInfo(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "user").(*db.User)
	capabilities, ok := capabilitySnapshotFromHTTP(r)
	if !ok {
		helpers.WriteErrorStatus(w, "CAPABILITY_CONTEXT_ERROR", http.StatusInternalServerError)
		return
	}

	var authMethods LoginAuthMethods

	authMethods.Totp = totpAuthMethod(capabilities)

	if util.Config.Mfa.Email.Enabled {
		authMethods.Email = &LoginEmailAuthMethod{}
	}

	timezone := util.Config.Schedule.Timezone

	if timezone == "" {
		timezone = "UTC"
	}

	roles, err := helpers.Store(r).GetGlobalRoles()
	if err != nil {
		log.WithFields(log.Fields{
			"context": "system_info",
			"user_id": user.ID,
		}).WithError(err).Error("Failed to get roles")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	globalAssignments, err := helpers.Store(r).GetGlobalRoleAssignments(user.ID)
	if err != nil {
		log.WithFields(log.Fields{
			"context": "system_info",
			"user_id": user.ID,
		}).WithError(err).Error("Failed to get global role assignments")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	effectiveGlobalPermissions := pro_interfaces.ExplainEffectiveGlobalPermissions(
		user.Admin, globalAssignments,
	)

	body := SystemInfo{
		Version:           util.Version(),
		Ansible:           util.AnsibleVersion(),
		WebHost:           util.Config.WebHost,
		UseRemoteRunner:   util.Config.IsUseRemoteRunner(),
		AuthMethods:       authMethods,
		LoginWithPassword: !util.Config.PasswordLoginDisable,
		Features:          proFeatures.GetFeatures(),
		SubscriptionState: "",
		GitClient:         util.Config.GitClientId,
		ScheduleTimezone:  timezone,
		Teams:             util.Config.Teams,
		Roles:             roles,
		BoltdbUsed:        util.Config.Dialect == "bolt",
		JWT: SystemInfoJWT{
			Enabled: util.Config.JWT.Enabled,
			MaxTTL:  util.Config.JWT.MaxTTL,
		},
		Edition:           pro_interfaces.Edition(util.BuildEdition),
		ContractVersion:   pro_interfaces.CoreContractVersion,
		Implementation:    util.EditionImplementation,
		CoreRevision:      util.CoreRevision,
		EnhancedRevision:  util.EnhancedRevision,
		Capabilities:      capabilities,
		GlobalPermissions: effectiveGlobalPermissions,
	}

	helpers.WriteJSON(w, http.StatusOK, body)
}

func totpAuthMethod(snapshot pro_interfaces.CapabilitySnapshot) *LoginTotpAuthMethod {
	switch snapshot.Decision(pro_interfaces.CapabilityTOTP).State() {
	case pro_interfaces.CapabilityStateDisabled,
		pro_interfaces.CapabilityStateShadow,
		pro_interfaces.CapabilityStateUnavailable:
		return nil
	default:
		return &LoginTotpAuthMethod{AllowRecovery: true}
	}
}
