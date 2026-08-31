package pro_interfaces

// CoreContractVersion identifies the public core-to-enhanced module contract.
// Implementations must publish the same value before they can be paired with
// this core revision.
const CoreContractVersion = "1.17.0"

// Edition identifies which enhanced-module implementation is active.
type Edition string

const (
	EditionCommunity Edition = "community"
	EditionEnhanced  Edition = "enhanced"
)

// Compatibility describes the edition implementation selected at build time.
// Source revisions are attached to release artifacts by the build pipeline;
// this value only describes the executable API contract.
type Compatibility struct {
	Edition         Edition `json:"edition"`
	ContractVersion string  `json:"contract_version"`
	Implementation  string  `json:"implementation"`
}
