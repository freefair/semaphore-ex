package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationOwnershipRejectsCollisionAndChangedShippedSQL(t *testing.T) {
	files := map[string]map[string]string{"2.20.2": {"v2.20.2.sql": "old"}, "2.20.66": {"v2.20.66.sql": "new"}}
	registered := map[string]bool{"2.20.2": true, "2.20.66": true}
	ledger := migrationLedger{Format: 1, Entries: []migrationEntry{{ID: "2.20.2", Registered: true, Owner: "fork", Files: files["2.20.2"]}, {ID: "2.20.66", Registered: true, Owner: "upstream-adapted", UpstreamID: "2.20.2", Rationale: "Preserve shipped fork ID", Files: files["2.20.66"]}}}
	require.NoError(t, validateLedger(ledger, files, registered))
	ledger.Entries[0].Files = map[string]string{"v2.20.2.sql": "changed"}
	assert.ErrorContains(t, validateLedger(ledger, files, registered), "SQL changed")
	ledger.Entries[0].Files = files["2.20.2"]
	ledger.Entries[0].Owner = "upstream"
	ledger.Entries[0].UpstreamID = "2.20.2"
	assert.ErrorContains(t, validateLedger(ledger, files, registered), "mapped twice")
}
func TestContractInventoryRejectsNewAndInheritedMethods(t *testing.T) {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "stub.go", `package sql; type WorkflowStoreImpl struct{}; func (*WorkflowStoreImpl) GetExpiredWorkflowDelays() {}`, 0)
	require.NoError(t, err)
	pkg, err := new(types.Config).Check("github.com/semaphoreui/semaphore/community-pro/db/sql", fs, []*ast.File{file}, nil)
	require.NoError(t, err)
	method := methodFor(pkg, "WorkflowStoreImpl", "GetExpiredWorkflowDelays")
	require.NotNil(t, method)
	assert.Equal(t, "community", classifyMethod(method))
	old := contractInventory{Format: 1, Symbols: []contractSymbol{{Name: "db/sql:WorkflowStoreImpl.GetExpiredWorkflowDelays", Signature: "func()", SelectedSignature: "func()", Implementation: "local"}}}
	current := contractInventory{Format: 1, Symbols: append([]contractSymbol{}, old.Symbols...)}
	current.Symbols[0].Implementation = "community"
	assert.ErrorContains(t, validateContracts(old, current), "inherited placeholders")
	current.Symbols[0] = old.Symbols[0]
	current.Symbols = append(current.Symbols, contractSymbol{Name: "db/sql:NewMethod"})
	assert.ErrorContains(t, validateContracts(old, current), "new exported contract")
}

// A concrete receiver can compile while its behavior silently comes from an embedded null store.
func TestMethodOriginDetectsActualPromotionAndExplicitOverride(t *testing.T) {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "stub.go", `package sql; type WorkflowStoreImpl struct{}; func (*WorkflowStoreImpl) Delay() {}`, 0)
	require.NoError(t, err)
	stub, err := new(types.Config).Check("github.com/semaphoreui/semaphore/community-pro/db/sql", fs, []*ast.File{file}, nil)
	require.NoError(t, err)
	for _, test := range []struct {
		name, extra, origin string
		depth               int
	}{{"inherited", "", "community", 2}, {"explicit", "func (*WorkflowStoreImpl) Delay() {}", "local", 1}} {
		t.Run(test.name, func(t *testing.T) {
			f, err := parser.ParseFile(fs, "enhanced.go", `package sql; import community "github.com/semaphoreui/semaphore/community-pro/db/sql"; type WorkflowStoreImpl struct {community.WorkflowStoreImpl}; `+test.extra, 0)
			require.NoError(t, err)
			config := types.Config{Importer: packageImporter{stub}}
			selected, err := config.Check("github.com/semaphoreui/semaphore/pro/db/sql", fs, []*ast.File{f}, nil)
			require.NoError(t, err)
			method := methodFor(selected, "WorkflowStoreImpl", "Delay")
			require.NotNil(t, method)
			assert.Equal(t, test.origin, classifyMethod(method))
			assert.Len(t, method.Index(), test.depth)
		})
	}
}

type packageImporter struct{ pkg *types.Package }

func (p packageImporter) Import(string) (*types.Package, error) { return p.pkg, nil }

func TestIncomingMigrationCollisionPreservesMappedIdentity(t *testing.T) {
	ledger := migrationLedger{Format: 1, Entries: []migrationEntry{{ID: "2.20.2", Owner: "fork"}, {ID: "2.20.4", Owner: "fork"}, {ID: "2.20.66", Owner: "upstream-adapted", UpstreamID: "2.20.2"}}}
	rows := unmappedMigrations(ledger, map[string]bool{"2.20.2": true, "2.20.4": true})
	require.Len(t, rows, 1)
	assert.Equal(t, "2.20.4", rows[0].UpstreamID)
	assert.Equal(t, "2.20.4", rows[0].LocalID)
	assert.Contains(t, rows[0].Action, "Collision")
}

func TestLedgerEvolutionRejectsChecksumRewriteButAllowsAppend(t *testing.T) {
	old := migrationLedger{Format: 1, Entries: []migrationEntry{{ID: "2.20.2", Owner: "fork", Files: map[string]string{"v2.20.2.sql": "shipped"}}}}
	current := migrationLedger{Format: 1, Entries: append([]migrationEntry{}, old.Entries...)}
	current.Entries = append(current.Entries, migrationEntry{ID: "2.20.68", Owner: "fork"})
	require.NoError(t, validateLedgerEvolution(old, current))
	current.Entries[0].Files = map[string]string{"v2.20.2.sql": "updated SQL and checksum together"}
	assert.ErrorContains(t, validateLedgerEvolution(old, current), "shipped migration")
	assert.Error(t, validateLedgerEvolution(old, migrationLedger{Format: 1}))
}

func TestChangedCallableRequiresRetainedBehavioralCoverage(t *testing.T) {
	old := contractInventory{Format: 1}
	next := contractInventory{Format: 1, Symbols: []contractSymbol{{Name: "db/sql:NewMethod", Signature: "func()", Implementation: "local"}}}
	assert.ErrorContains(t, validateContractEvolution(old, next), "requires behavior_tests")
	next.Symbols[0].BehaviorTests = []string{"path_test.go::TestPersistence"}
	require.NoError(t, validateContractEvolution(old, next))
	old = next
	next = contractInventory{Format: 1, Symbols: append([]contractSymbol{}, old.Symbols...)}
	next.Symbols[0].BehaviorTests = nil
	assert.ErrorContains(t, validateContractEvolution(old, next), "coverage removed")
}

func TestRemovingExportAndInventoryTogetherCannotBypassBaseline(t *testing.T) {
	old := contractInventory{Format: 1, Symbols: []contractSymbol{{Name: "db/sql:DurableMethod", Signature: "func()", Implementation: "local"}}}
	assert.ErrorContains(t, validateContractEvolution(old, contractInventory{Format: 1}), "exported contract")
}
