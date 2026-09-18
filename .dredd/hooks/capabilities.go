package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/tools/dreddhooks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/snikch/goodman/hooks"
	trans "github.com/snikch/goodman/transaction"
)

// STATE
// Runtime created objects we need to reference in test setups
var testRunnerUser *db.User
var userPathTestUser *db.User
var userProject *db.Project
var userKey *db.AccessKey
var task *db.Task
var schedule *db.Schedule
var view *db.View
var integration *db.Integration
var integrationextractvalue *db.IntegrationExtractValue
var integrationmatch *db.IntegrationMatcher
var invite *db.ProjectInvite
var runner *db.Runner
var globalRunner *db.Runner
var workflow *db.WorkflowTemplate
var workflowRun *db.WorkflowRun
var workflowApproval *db.WorkflowApproval

// Runtime created simple ID values for some items we need to reference in other objects
var repoID int
var inventoryID int
var environmentID int
var templateID int
var integrationID int
var integrationExtractValueID int
var integrationMatchID int
var workflowID int
var workflowRunID int
var workflowNodeID int

var capabilities = map[string][]string{
	"user":                    {},
	"project":                 {"user"},
	"repository":              {"access_key"},
	"inventory":               {"repository"},
	"environment":             {"repository"},
	"template":                {"repository", "inventory", "environment", "view"},
	"task":                    {"template"},
	"schedule":                {"template"},
	"view":                    {},
	"integration":             {"project", "template"},
	"integrationextractvalue": {"integration"},
	"integrationmatcher":      {"integration"},
	"invite":                  {"user", "project"},
	"runner":                  {"project"},
	"global_runner":           {},
	"workflow":                {"template"},
	"workflow_run":            {"workflow"},
	"workflow_approval":       {"workflow_run"},
}

func capabilityWrapper(cap string) func(t *trans.Transaction) {
	return func(t *trans.Transaction) {
		addCapabilities([]string{cap})
	}
}

func addCapabilities(caps []string) {
	dbConnect()
	defer store.Close()
	resolved := make([]string, 0)
	uid := getUUID()
	resolveCapability(caps, resolved, uid)
}

func resolveCapability(caps []string, resolved []string, uid string) {
	for _, v := range caps {

		//if cap has deps resolve them
		if val, ok := capabilities[v]; ok {
			resolveCapability(val, resolved, uid)
		}

		//skip if already resolved
		if _, exists := stringInSlice(v, resolved); exists {
			continue
		}

		//Add dep specific stuff
		switch v {
		case "invite":
			invite = addInvite()
		case "schedule":
			schedule = addSchedule()
		case "view":
			view = addView()
		case "user":
			userPathTestUser = addUser()
		case "project":
			userProject = addProject()
			//allow the admin user (test executor) to manipulate the project
			addUserProjectRelation(userProject.ID, testRunnerUser.ID)
			addUserProjectRelation(userProject.ID, userPathTestUser.ID)
		case "access_key":
			userKey = addAccessKey(&userProject.ID)
		case "repository":
			pRepo, err := store.CreateRepository(db.Repository{
				ProjectID: userProject.ID,
				GitURL:    "git@github.com/ansible,semaphore/semaphore",
				GitBranch: "develop",
				SSHKeyID:  userKey.ID,
				Name:      "ITR-" + uid,
			})
			printError(err)
			repoID = pRepo.ID
		case "inventory":
			res, err := store.CreateInventory(db.Inventory{
				ProjectID:    userProject.ID,
				Name:         "ITI-" + uid,
				Type:         "static",
				SSHKeyID:     &userKey.ID,
				BecomeKeyID:  &userKey.ID,
				Inventory:    "Test Inventory",
				RepositoryID: &repoID,
			})
			printError(err)
			inventoryID = res.ID
		case "environment":
			pwd := "test-pass"
			env := "{}"
			res, err := store.CreateEnvironment(db.Environment{
				ProjectID: userProject.ID,
				Name:      "ITI-" + uid,
				JSON:      "{}",
				Password:  &pwd,
				ENV:       &env,
			})
			printError(err)
			environmentID = res.ID
		case "template":
			args := "[]"
			desc := "Hello, World!"
			branch := "main"
			res, err := store.CreateTemplate(db.Template{
				ProjectID:               userProject.ID,
				InventoryID:             &inventoryID,
				RepositoryID:            repoID,
				EnvironmentIDs:          []int{environmentID},
				Name:                    "Test-" + uid,
				Playbook:                "test-playbook.yml",
				Arguments:               &args,
				AllowOverrideArgsInTask: false,
				Description:             &desc,
				ViewID:                  &view.ID,
				App:                     db.AppAnsible,
				GitBranch:               &branch,
				SurveyVars:              []db.SurveyVar{},
			})

			printError(err)
			templateID = res.ID
		case "task":
			task = addTask()
		case "integration":
			integration = addIntegration()
			integrationID = integration.ID
		case "integrationextractvalue":
			integrationextractvalue = addIntegrationExtractValue()
			integrationExtractValueID = integrationextractvalue.ID
		case "integrationmatcher":
			integrationmatch = addIntegrationMatcher()
			integrationMatchID = integrationmatch.ID
		case "runner":
			runner = addRunner()
		case "global_runner":
			globalRunner = addGlobalRunner()
		case "workflow":
			workflow = addWorkflow()
			workflowID = workflow.ID
		case "workflow_run":
			workflowRun = addWorkflowRun()
			workflowRunID = workflowRun.ID
		case "workflow_approval":
			workflowApproval = addWorkflowApproval()
			workflowNodeID = workflowApproval.WorkflowNodeID
		default:
			panic("unknown capability " + v)
		}
		resolved = append(resolved, v)
	}
}

// HOOKS
var skipTest = func(t *trans.Transaction) {
	t.Skip = true
}

// Contains all the substitutions for paths under test
// The parameter example value in the api-doc should respond to the index+1 of the function in this slice
// ie the project id, with example value 1, will be replaced by the return value of pathSubPatterns[0]
var pathSubPatterns = []func() string{
	func() string { return strconv.Itoa(userProject.ID) },
	func() string { return strconv.Itoa(userPathTestUser.ID) },
	func() string { return strconv.Itoa(userKey.ID) },
	func() string { return strconv.Itoa(repoID) },
	func() string { return strconv.Itoa(inventoryID) },
	func() string { return strconv.Itoa(environmentID) },
	func() string { return strconv.Itoa(templateID) },
	func() string { return strconv.Itoa(task.ID) },
	func() string { return strconv.Itoa(schedule.ID) },
	func() string { return strconv.Itoa(view.ID) },
	func() string { return strconv.Itoa(integration.ID) },
	func() string { return strconv.Itoa(integrationextractvalue.ID) },
	func() string { return strconv.Itoa(integrationmatch.ID) },
	func() string { return strconv.Itoa(invite.ID) }, // invite_id, x-example: 14
	// alias_id, x-example: 15 — integration aliases are not set up by these
	// hooks, so leave the path segment untouched (kept here only to preserve
	// the positional mapping of the entries that follow).
	func() string { return strconv.Itoa(15) },
	func() string {
		if runner == nil {
			return "0"
		}
		return strconv.Itoa(runner.ID)
	}, // runner_id (project), x-example: 16
	func() string { return strconv.Itoa(globalRunner.ID) }, // global runner_id, x-example: 17
	func() string {
		if workflow == nil {
			return "0"
		}
		return strconv.Itoa(workflow.ID)
	}, // workflow_id, x-example: 18
	func() string {
		if workflowRun == nil {
			return "0"
		}
		return strconv.Itoa(workflowRun.ID)
	}, // run_id, x-example: 19
	func() string {
		return strconv.Itoa(workflowNodeID)
	}, // node_id, x-example: 20
}

// alterRequestPath with the above slice of functions
func alterRequestPath(t *trans.Transaction) {
	pathArgs := strings.Split(t.FullPath, "/")
	exploded := make([]string, len(pathArgs))
	copy(exploded, pathArgs)
	for k, v := range pathSubPatterns {

		pos, exists := stringInSlice(strconv.Itoa(k+1), exploded)
		if exists {
			pathArgs[pos] = v()
		}
	}
	t.FullPath = strings.Join(pathArgs, "/")

	t.Request.URI = t.FullPath
}

func alterRequestBody(t *trans.Transaction) {
	var request map[string]any
	json.Unmarshal([]byte(t.Request.Body), &request)

	if userProject != nil {
		bodyFieldProcessor("project_id", userProject.ID, &request)
	}
	bodyFieldProcessor("json", "{}", &request)
	if strings.Contains(t.FullPath, "/tasks") {
		bodyFieldProcessor("environment", "{}", &request)
		bodyFieldProcessor("arguments", "[]", &request)
	}
	if userKey != nil {
		bodyFieldProcessor("ssh_key_id", userKey.ID, &request)
		bodyFieldProcessor("become_key_id", userKey.ID, &request)
		// IntegrationRequest.auth_secret_id references an access key for
		// token/HMAC webhook verification. Without a valid key reference,
		// integration POST/PUT fail on the access_key FK constraint.
		bodyFieldProcessor("auth_secret_id", userKey.ID, &request)
	}
	if invite != nil {
		bodyFieldProcessor("invite_id", 4, &request)
	}
	if t.Request.Method == "PUT" && strings.Contains(t.Request.URI, "/templates/") {
		bodyFieldProcessor("name", "Test-"+getUUID(), &request)
	}

	bodyFieldProcessor("environment_id", environmentID, &request)
	bodyFieldProcessor("environment_ids", []int{environmentID}, &request)
	bodyFieldProcessor("inventory_id", inventoryID, &request)
	bodyFieldProcessor("repository_id", repoID, &request)
	bodyFieldProcessor("template_id", templateID, &request)
	bodyFieldProcessor("build_template_id", nil, &request)
	if task != nil {
		bodyFieldProcessor("task_id", task.ID, &request)
	}
	if schedule != nil {
		bodyFieldProcessor("schedule_id", schedule.ID, &request)
	}
	if view != nil {
		bodyFieldProcessor("view_id", view.ID, &request)
	}

	if integration != nil {
		bodyFieldProcessor("integration_id", integration.ID, &request)
	}
	if integrationextractvalue != nil {
		bodyFieldProcessor("value_id", integrationextractvalue.ID, &request)
	}
	if integrationmatch != nil {
		bodyFieldProcessor("matcher_id", integrationmatch.ID, &request)
	}

	// Inject object ID to body for PUT requests
	if strings.ToLower(t.Request.Method) == "put" {
		if objectID, ok := dreddhooks.PutRequestObjectID(t.FullPath); ok {
			request["id"] = objectID
		}
	}

	out, _ := json.Marshal(request)
	t.Request.Body = string(out)
}

func bodyFieldProcessor(id string, sub any, request *map[string]any) {
	if _, ok := (*request)[id]; ok {
		(*request)[id] = sub
	}
}

type terraformDreddFixture struct {
	workspace             db.Inventory
	credential            db.AccessKey
	replacementCredential db.AccessKey
	alias                 db.TerraformInventoryAlias
	initialState          string
	postedState           string
}

type terraformStateWriter interface {
	PutTerraformState(projectID, inventoryID int, ciphertext, lockID string) error
	GetLatestTerraformState(projectID, inventoryID int) (db.TerraformInventoryState, error)
}

type terraformFixtureRegistration struct {
	name       string
	backend    bool
	needsState bool
}

func registerTerraformDreddFixtures(h *hooks.Hooks) func() {
	registrations := []terraformFixtureRegistration{
		{"terraform-state > /api/terraform/{alias} > Read the current encrypted Terraform HTTP backend state > 200 > application/json", true, true},
		{"terraform-state > /api/terraform/{alias} > Append a new encrypted Terraform HTTP backend state version > 200 > application/json", true, false},
		{"terraform-state > /api/terraform/{alias} > Append a deletion tombstone for the current Terraform state > 200 > application/json", true, true},
		{"terraform-state > /api/project/{project_id}/inventory/{inventory_id}/terraform/aliases > List Terraform HTTP backend aliases for one workspace inventory > 200 > application/json", false, false},
		{"terraform-state > /api/project/{project_id}/inventory/{inventory_id}/terraform/aliases > Create a generated Terraform HTTP backend alias > 201 > application/json", false, false},
		{"terraform-state > /api/project/{project_id}/inventory/{inventory_id}/terraform/aliases/{alias_id} > Read Terraform HTTP backend alias metadata > 200 > application/json", false, false},
		{"terraform-state > /api/project/{project_id}/inventory/{inventory_id}/terraform/aliases/{alias_id} > Change the project login-password credential used by an alias > 200 > application/json", false, false},
		{"terraform-state > /api/project/{project_id}/inventory/{inventory_id}/terraform/aliases/{alias_id} > Delete a Terraform HTTP backend alias > 204 > application/json", false, false},
	}

	fixtures := make([]terraformDreddFixture, len(registrations))
	for index := range registrations {
		registration := registrations[index]
		fixture := &fixtures[index]
		h.Before(registration.name, func(t *trans.Transaction) {
			fixture.setup(registration, t)
		})
		h.After(registration.name, func(t *trans.Transaction) {
			fixture.assertResult(registration, t)
		})
	}

	return func() {
		for index := range registrations {
			registration := registrations[index]
			fixture := &fixtures[index]
			h.Before(registration.name, func(t *trans.Transaction) {
				fixture.configureRequest(registration, t)
			})
		}
	}
}

func (fixture *terraformDreddFixture) setup(registration terraformFixtureRegistration, t *trans.Transaction) {
	if t.Skip {
		return
	}
	if userProject == nil || userProject.ID <= 0 {
		panic("terraform Dredd fixture requires a project")
	}
	if util.Config == nil || !util.Config.AccessKeyEncryptionEnabled() {
		panic("terraform Dredd fixture requires access-key encryption")
	}

	dbConnect()
	defer store.Close()

	workspace, err := store.CreateInventory(db.Inventory{
		ProjectID: userProject.ID,
		Name:      "ITTW-" + getUUID(),
		Type:      db.InventoryTerraformWorkspace,
	})
	if err != nil {
		panic(fmt.Errorf("create Terraform workspace fixture: %w", err))
	}
	fixture.workspace = workspace

	fixture.credential = createTerraformDreddCredential("terraform-dredd", "terraform-dredd-password")

	terraformStore := proFactory.NewTerraformStore(store)
	alias := db.TerraformInventoryAlias{
		ProjectID: userProject.ID, InventoryID: workspace.ID, AuthKeyID: fixture.credential.ID,
		Alias: "terraform-dredd-" + getUUID(),
	}
	created, err := terraformStore.CreateTerraformInventoryAlias(alias)
	if err != nil {
		panic(fmt.Errorf("create Terraform backend alias: %w", err))
	}
	if created != alias {
		panic("Terraform backend alias creation returned unexpected data")
	}
	persisted, err := terraformStore.GetTerraformInventoryAlias(userProject.ID, workspace.ID, alias.Alias)
	if err != nil {
		panic(fmt.Errorf("read Terraform backend alias fixture: %w", err))
	}
	if persisted != alias {
		panic("Terraform backend alias fixture differs from its persisted value")
	}
	fixture.alias = persisted

	if t.Request.Method == "PUT" {
		fixture.replacementCredential = createTerraformDreddCredential("terraform-dredd-replacement", "terraform-dredd-replacement-password")
	}

	if !registration.needsState {
		return
	}
	stateWriter, ok := terraformStore.(terraformStateWriter)
	if !ok {
		panic("Terraform backend store does not support state fixtures")
	}
	fixture.initialState = `{"version":4,"serial":1,"lineage":"terraform-dredd"}`
	ciphertext, err := util.Config.EncryptAccessSecret([]byte(fixture.initialState))
	if err != nil {
		panic(fmt.Errorf("encrypt Terraform backend state: %w", err))
	}
	if util.SecretKeyID(ciphertext) == "" {
		panic("Terraform backend state was not encrypted")
	}
	if err = stateWriter.PutTerraformState(userProject.ID, workspace.ID, ciphertext, ""); err != nil {
		panic(fmt.Errorf("create Terraform backend state: %w", err))
	}
	state, err := stateWriter.GetLatestTerraformState(userProject.ID, workspace.ID)
	if err != nil || state.ID <= 0 || state.State != ciphertext {
		panic(fmt.Errorf("verify Terraform backend state fixture: %w", err))
	}
}

func createTerraformDreddCredential(login, password string) db.AccessKey {
	credential := db.AccessKey{
		Name:      "ITTK-" + getUUID(),
		Type:      db.AccessKeyLoginPassword,
		ProjectID: &userProject.ID,
		LoginPassword: db.LoginPassword{
			Login:    login,
			Password: password,
		},
	}
	if err := server.NewLocalAccessKeyDeserializer().SerializeSecret(&credential); err != nil {
		panic(fmt.Errorf("encrypt Terraform backend credential: %w", err))
	}
	if credential.Secret == nil || util.SecretKeyID(*credential.Secret) == "" {
		panic("Terraform backend credential was not encrypted")
	}
	created, err := store.CreateAccessKey(credential)
	if err != nil {
		panic(fmt.Errorf("create Terraform backend credential: %w", err))
	}
	return created
}

func (fixture *terraformDreddFixture) configureRequest(registration terraformFixtureRegistration, t *trans.Transaction) {
	if t.Skip {
		return
	}
	if registration.backend {
		t.FullPath = "/api/terraform/" + fixture.alias.Alias
		t.Request.URI = t.FullPath
		if t.Request.Headers == nil {
			t.Request.Headers = map[string]interface{}{}
		}
		t.Request.Headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(fixture.credential.LoginPassword.Login+":"+fixture.credential.LoginPassword.Password))
		if t.Request.Method == "POST" {
			fixture.postedState = `{"version":4,"serial":2,"lineage":"terraform-dredd"}`
			t.Request.Body = fixture.postedState
		}
		return
	}

	path := fmt.Sprintf("/api/project/%d/inventory/%d/terraform/aliases", userProject.ID, fixture.workspace.ID)
	if strings.Contains(t.FullPath, "/aliases/") {
		path += "/" + fixture.alias.Alias
	}
	t.FullPath = path
	t.Request.URI = path
	if t.Request.Method == "POST" || t.Request.Method == "PUT" {
		credential := fixture.credential
		if t.Request.Method == "PUT" {
			credential = fixture.replacementCredential
			if credential.ID <= 0 || credential.ID == fixture.credential.ID {
				panic("Terraform alias update fixture requires a replacement credential")
			}
		}
		t.Request.Body = fmt.Sprintf(`{"auth_key_id":%d}`, credential.ID)
	}
}

func (fixture *terraformDreddFixture) assertResult(registration terraformFixtureRegistration, t *trans.Transaction) {
	if t.Skip || t.Real == nil || t.Real.StatusCode < 200 || t.Real.StatusCode >= 300 {
		return
	}
	if registration.backend {
		fixture.assertBackendResult(t)
		return
	}
	fixture.assertAliasResult(t)
}

func (fixture *terraformDreddFixture) assertBackendResult(t *trans.Transaction) {
	switch t.Request.Method {
	case "GET":
		if t.Real.Body != fixture.initialState {
			failTerraformDredd(t, "Terraform state GET response did not match the fixture state")
		}
	case "POST":
		state, err := fixture.latestState()
		if err != nil {
			failTerraformDredd(t, fmt.Sprintf("read Terraform state after POST: %v", err))
			return
		}
		plain, err := util.Config.DecryptAccessSecret(state.State)
		if err != nil || string(plain) != fixture.postedState {
			failTerraformDredd(t, "Terraform state POST did not persist the submitted encrypted state")
		}
	case "DELETE":
		_, err := fixture.latestState()
		if !errors.Is(err, db.ErrNotFound) {
			failTerraformDredd(t, "Terraform state DELETE did not create a state tombstone")
		}
	}
}

func (fixture *terraformDreddFixture) assertAliasResult(t *trans.Transaction) {
	switch t.Request.Method {
	case "GET":
		if strings.HasSuffix(t.FullPath, "/aliases") {
			var aliases []map[string]any
			if err := json.Unmarshal([]byte(t.Real.Body), &aliases); err != nil || !containsTerraformAlias(aliases, fixture.alias) {
				failTerraformDredd(t, "Terraform alias list response did not contain the fixture alias metadata")
			}
			return
		}
		if !matchesTerraformAliasResponse(t.Real.Body, fixture.alias) {
			failTerraformDredd(t, "Terraform alias GET response did not match the fixture alias metadata")
		}
	case "POST":
		var response struct {
			ID        string `json:"id"`
			AuthKeyID int    `json:"auth_key_id"`
		}
		if err := json.Unmarshal([]byte(t.Real.Body), &response); err != nil || response.ID == "" || response.AuthKeyID != fixture.credential.ID {
			failTerraformDredd(t, "Terraform alias POST response did not return generated metadata")
			return
		}
		persisted, err := fixture.readAlias(response.ID)
		if err != nil || persisted.AuthKeyID != fixture.credential.ID || persisted.ProjectID != userProject.ID || persisted.InventoryID != fixture.workspace.ID {
			failTerraformDredd(t, fmt.Sprintf("read Terraform alias created by POST: %v", err))
		}
	case "PUT":
		persisted, err := fixture.readAlias(fixture.alias.Alias)
		if err != nil || persisted.AuthKeyID != fixture.replacementCredential.ID || !matchesTerraformAliasResponse(t.Real.Body, persisted) {
			failTerraformDredd(t, "Terraform alias PUT did not persist the replacement credential")
		}
	case "DELETE":
		_, err := fixture.readAlias(fixture.alias.Alias)
		if !errors.Is(err, db.ErrNotFound) {
			failTerraformDredd(t, "Terraform alias DELETE did not remove the fixture alias")
		}
	}
}

func (fixture *terraformDreddFixture) latestState() (db.TerraformInventoryState, error) {
	dbConnect()
	defer store.Close()
	stateWriter, ok := proFactory.NewTerraformStore(store).(terraformStateWriter)
	if !ok {
		return db.TerraformInventoryState{}, errors.New("Terraform backend store does not support state fixtures")
	}
	return stateWriter.GetLatestTerraformState(userProject.ID, fixture.workspace.ID)
}

func (fixture *terraformDreddFixture) readAlias(alias string) (db.TerraformInventoryAlias, error) {
	dbConnect()
	defer store.Close()
	return proFactory.NewTerraformStore(store).GetTerraformInventoryAlias(userProject.ID, fixture.workspace.ID, alias)
}

func containsTerraformAlias(aliases []map[string]any, expected db.TerraformInventoryAlias) bool {
	for _, alias := range aliases {
		if alias["id"] == expected.Alias && alias["project_id"] == float64(expected.ProjectID) &&
			alias["inventory_id"] == float64(expected.InventoryID) && alias["auth_key_id"] == float64(expected.AuthKeyID) {
			return true
		}
	}
	return false
}

func matchesTerraformAliasResponse(body string, expected db.TerraformInventoryAlias) bool {
	var alias map[string]any
	if err := json.Unmarshal([]byte(body), &alias); err != nil {
		return false
	}
	return alias["id"] == expected.Alias && alias["project_id"] == float64(expected.ProjectID) &&
		alias["inventory_id"] == float64(expected.InventoryID) && alias["auth_key_id"] == float64(expected.AuthKeyID)
}

func failTerraformDredd(t *trans.Transaction, message string) {
	t.Fail = message
}
