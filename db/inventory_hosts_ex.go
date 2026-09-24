package db

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

// InventoryHostEvent carries names only. Runner output and host variables never
// form part of this transport or its persisted inventory projection.
type InventoryHostEvent struct {
	Version int      `json:"version"`
	Kind    string   `json:"event"`
	Host    string   `json:"host,omitempty"`
	Groups  []string `json:"groups,omitempty"`
	Count   int      `json:"count,omitempty"`
}

func (e InventoryHostEvent) Validate() error {
	if e.Version != 1 || e.Count < 0 || e.Count > 100000 || len(e.Groups) > 256 {
		return errors.New("invalid inventory result bounds")
	}
	switch e.Kind {
	case "start", "complete", "error":
		if e.Host != "" || len(e.Groups) != 0 {
			return errors.New("invalid inventory result fields")
		}
	case "host":
		if !validInventoryName(e.Host) {
			return errors.New("invalid inventory host name")
		}
		for _, group := range e.Groups {
			if !validInventoryName(group) {
				return errors.New("invalid inventory group name")
			}
		}
	default:
		return errors.New("invalid inventory result kind")
	}
	return nil
}

func validInventoryName(value string) bool {
	return value != "" && len(value) <= 255 && strings.IndexFunc(value, unicode.IsControl) < 0
}

// IsInventoryRefresh identifies the restricted Ansible operation in persisted
// task parameters, including tasks reconstructed by another HA server.
func (task Task) IsInventoryRefresh() bool {
	refresh, _ := task.Params["inventory_refresh"].(bool)
	return refresh
}

type InventoryHostSnapshot struct {
	TaskID        int       `json:"task_id" db:"task_id"`
	Generation    int       `json:"generation" db:"generation"`
	ProjectID     int       `json:"project_id" db:"project_id"`
	InventoryID   int       `json:"inventory_id" db:"inventory_id"`
	InventoryName string    `json:"inventory_name" db:"inventory_name"`
	TemplateID    int       `json:"template_id" db:"template_id"`
	State         string    `json:"state" db:"state"`
	HostCount     int       `json:"host_count" db:"host_count"`
	Created       time.Time `json:"created" db:"created"`
}

type InventoryHost struct {
	InventoryID   int              `json:"inventory_id" db:"inventory_id"`
	InventoryName string           `json:"inventory_name" db:"inventory_name"`
	Host          string           `json:"host" db:"host"`
	Groups        StringArrayField `json:"groups" db:"groups_json"`
	TaskID        int              `json:"snapshot_task_id" db:"task_id"`
	ResolvedAt    time.Time        `json:"resolved_at" db:"resolved_at"`
}

type InventoryHostQuery struct {
	InventoryID      int
	Search           string
	AfterInventoryID int
	AfterHost        string
	Count            int
}

type InventoryHostPage struct {
	Items           []InventoryHost `json:"items"`
	NextInventoryID int             `json:"next_inventory_id,omitempty"`
	NextHost        string          `json:"next_host,omitempty"`
}

// InventoryHostRepository persists per-assignment snapshots independently of
// mutable template inventory selection. Readers always provide project scope.
type InventoryHostRepository interface {
	IngestInventoryHostEvent(task Task, inventory Inventory, event InventoryHostEvent) error
	GetInventoryHostSnapshots(projectID, inventoryID int) ([]InventoryHostSnapshot, error)
	GetInventoryHosts(projectID int, query InventoryHostQuery) (InventoryHostPage, error)
	GetHostTaskIDs(projectID, inventoryID int, host string, beforeID, count int) ([]int, error)
}
