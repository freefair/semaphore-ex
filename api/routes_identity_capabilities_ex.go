package api

import "github.com/gorilla/mux"

// registerIdentityCapabilityRoutes keeps the identity capability registrations
// under the existing admin middleware and in their original route order.
func registerIdentityCapabilityRoutes(
	adminAPI *mux.Router,
	totpController *TOTPController,
	ldapController *LDAPController,
	oidcGroupMappingController *OIDCGroupMappingController,
) {
	adminAPI.Path("/capabilities/totp").HandlerFunc(totpController.Configure).Methods("PUT")
	adminAPI.Path("/capabilities/totp").HandlerFunc(totpController.Configuration).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/totp/transitions").HandlerFunc(totpController.Transitions).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/ldap").HandlerFunc(ldapController.Providers).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/ldap").HandlerFunc(ldapController.Configure).Methods("PUT")
	adminAPI.Path("/capabilities/ldap/test").HandlerFunc(ldapController.Test).Methods("POST")
	adminAPI.Path("/capabilities/ldap/state").HandlerFunc(ldapController.SetState).Methods("PUT")
	adminAPI.Path("/capabilities/ldap/transitions").HandlerFunc(ldapController.Transitions).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/ldap/group-mappings").HandlerFunc(ldapController.GroupMappings).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/ldap/group-mappings/{mapping_id}").HandlerFunc(ldapController.SaveGroupMapping).Methods("PUT")
	adminAPI.Path("/capabilities/ldap/group-mappings/{mapping_id}").HandlerFunc(ldapController.DeleteGroupMapping).Methods("DELETE")
	adminAPI.Path("/capabilities/ldap/group-mappings/preview").HandlerFunc(ldapController.PreviewGroupMappings).Methods("POST")
	adminAPI.Path("/capabilities/ldap/group-mappings/apply").HandlerFunc(ldapController.ApplyGroupPreview).Methods("POST")
	adminAPI.Path("/capabilities/ldap/group-mappings/reconcile").HandlerFunc(ldapController.ReconcileGroupMappings).Methods("POST")
	adminAPI.Path("/capabilities/ldap/group-mappings/history").HandlerFunc(ldapController.GroupReconciliationHistory).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/oidc/group-mapping/providers").HandlerFunc(oidcGroupMappingController.Providers).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/oidc/group-mappings").HandlerFunc(oidcGroupMappingController.GroupMappings).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/oidc/group-mappings/{mapping_id}").HandlerFunc(oidcGroupMappingController.SaveGroupMapping).Methods("PUT")
	adminAPI.Path("/capabilities/oidc/group-mappings/{mapping_id}").HandlerFunc(oidcGroupMappingController.DeleteGroupMapping).Methods("DELETE")
	adminAPI.Path("/capabilities/oidc/group-mappings/preview").HandlerFunc(oidcGroupMappingController.PreviewGroupMappings).Methods("POST")
	adminAPI.Path("/capabilities/oidc/group-mappings/history").HandlerFunc(oidcGroupMappingController.GroupReconciliationHistory).Methods("GET", "HEAD")
	adminAPI.Path("/capabilities/oidc/group-mappings/assignments").HandlerFunc(oidcGroupMappingController.EffectiveGroupAssignments).Methods("GET", "HEAD")
}
