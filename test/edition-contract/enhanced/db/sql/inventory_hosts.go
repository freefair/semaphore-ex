package sql

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

var _ db.InventoryHostRepository = (*AnsibleTaskStoreImpl)(nil)

func (d *AnsibleTaskStoreImpl) IngestInventoryHostEvent(task db.Task, inventory db.Inventory, event db.InventoryHostEvent) error {
	if d.connection == nil {
		return errTaskSummaryRepositoryUnavailable
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if inventory.ID <= 0 || inventory.ProjectID != task.ProjectID {
		return errors.New("inventory snapshot scope does not match task")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	count, err := tx.SelectInt(d.connection.PrepareQuery("select count(1) from task where project_id=? and id=? and template_id=? and assignment_generation=?"), task.ProjectID, task.ID, task.TemplateID, task.AssignmentGeneration)
	if err != nil {
		return err
	}
	if count != 1 {
		return db.ErrNotFound
	}
	_, err = tx.Exec(insertIgnoreQuery(d.connection, `insert into inventory__snapshot
		(task_id, generation, project_id, inventory_id, inventory_name, template_id, state, host_count, created)
		values (?, ?, ?, ?, ?, ?, 'collecting', -1, ?)`, "task_id, generation"),
		task.ID, task.AssignmentGeneration, task.ProjectID, inventory.ID, inventory.Name, task.TemplateID, time.Now().UTC())
	if err != nil {
		return err
	}
	// The no-op update locks this assignment's header before membership and
	// completion are checked, including concurrent HA ingestion of split batches.
	_, err = tx.Exec(d.connection.PrepareQuery("update inventory__snapshot set state=state where task_id=? and generation=?"), task.ID, task.AssignmentGeneration)
	if err != nil {
		return err
	}
	var state string
	if err = tx.SelectOne(&state, d.connection.PrepareQuery("select state from inventory__snapshot where task_id=? and generation=?"), task.ID, task.AssignmentGeneration); err != nil {
		return err
	}
	// Terminal snapshots are immutable. The runner may replay buffered output
	// after completing a task, but that output must not revise the membership or
	// turn a successful resolution into an error.
	if state == "ready" || state == "error" {
		return tx.Commit()
	}
	switch event.Kind {
	case "host":
		groups, marshalErr := json.Marshal(event.Groups)
		if marshalErr != nil {
			return marshalErr
		}
		result, insertErr := tx.Exec(insertIgnoreQuery(d.connection, "insert into inventory__snapshot_host (task_id, generation, host, groups_json) values (?, ?, ?, ?)", "task_id, generation, host"), task.ID, task.AssignmentGeneration, event.Host, string(groups))
		if insertErr != nil {
			return insertErr
		}
		inserted, countErr := result.RowsAffected()
		if countErr != nil {
			return countErr
		}
		if inserted > 0 {
			_, err = tx.Exec(d.connection.PrepareQuery("update inventory__snapshot set received_hosts=received_hosts+1 where task_id=? and generation=?"), task.ID, task.AssignmentGeneration)
		}
	case "complete":
		_, err = tx.Exec(d.connection.PrepareQuery("update inventory__snapshot set host_count=? where task_id=? and generation=? and state!='error'"), event.Count, task.ID, task.AssignmentGeneration)
	case "error":
		_, err = tx.Exec(d.connection.PrepareQuery("update inventory__snapshot set state='error' where task_id=? and generation=? and state!='ready'"), task.ID, task.AssignmentGeneration)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(d.connection.PrepareQuery(`update inventory__snapshot set state='ready'
		where task_id=? and generation=? and state='collecting' and host_count>=0
		and host_count=received_hosts`), task.ID, task.AssignmentGeneration)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *AnsibleTaskStoreImpl) GetInventoryHostSnapshots(projectID, inventoryID int) ([]db.InventoryHostSnapshot, error) {
	items := []db.InventoryHostSnapshot{}
	if d.connection == nil {
		return items, errTaskSummaryRepositoryUnavailable
	}
	_, err := d.connection.SelectAll(&items, `select s.task_id, s.generation, s.project_id, s.inventory_id, s.inventory_name, s.template_id,
		case when s.state='collecting' and t.status in ('success','error','stopped','blocked') then 'error' else s.state end as state,
		s.host_count, s.created from inventory__snapshot s join task t on t.id=s.task_id
		where s.project_id=? and s.inventory_id=? order by s.created desc, s.task_id desc, s.generation desc limit 20`, projectID, inventoryID)
	return items, err
}

func (d *AnsibleTaskStoreImpl) GetInventoryHosts(projectID int, params db.InventoryHostQuery) (db.InventoryHostPage, error) {
	page := db.InventoryHostPage{Items: []db.InventoryHost{}}
	if d.connection == nil {
		return page, errTaskSummaryRepositoryUnavailable
	}
	query := `select s.inventory_id, i.name as inventory_name, h.host, h.groups_json, s.task_id, s.created as resolved_at
		from inventory__snapshot s join inventory__snapshot_host h on h.task_id=s.task_id and h.generation=s.generation
		join project__inventory i on i.id=s.inventory_id and i.project_id=s.project_id
		where s.project_id=? and s.state='ready'
		and not exists (select 1 from inventory__snapshot newer where newer.project_id=s.project_id and newer.inventory_id=s.inventory_id
		and newer.state='ready' and (newer.created>s.created or (newer.created=s.created and newer.task_id>s.task_id)
		or (newer.created=s.created and newer.task_id=s.task_id and newer.generation>s.generation)))`
	args := []any{projectID}
	if params.InventoryID > 0 {
		query += " and s.inventory_id=?"
		args = append(args, params.InventoryID)
	}
	if params.Search != "" {
		query += " and (lower(h.host) like ? escape '!' or lower(i.name) like ? escape '!')"
		search := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(params.Search)) + "%"
		args = append(args, search, search)
	}
	if params.AfterInventoryID > 0 {
		query += " and (s.inventory_id>? or (s.inventory_id=? and h.host>?))"
		args = append(args, params.AfterInventoryID, params.AfterInventoryID, params.AfterHost)
	}
	count := summaryPageSize(params.Count)
	query += " order by s.inventory_id, h.host limit ?"
	args = append(args, count+1)
	if _, err := d.connection.SelectAll(&page.Items, query, args...); err != nil {
		return page, err
	}
	if len(page.Items) > count {
		page.Items = page.Items[:count]
		last := page.Items[count-1]
		page.NextInventoryID, page.NextHost = last.InventoryID, last.Host
	}
	return page, nil
}

func (d *AnsibleTaskStoreImpl) GetHostTaskIDs(projectID, inventoryID int, host string, beforeID, count int) ([]int, error) {
	items := []int{}
	if d.connection == nil {
		return items, errTaskSummaryRepositoryUnavailable
	}
	// Existing reviewed runs already carry a private immutable inventory
	// descriptor. Use that for older history, never today's template setting.
	type candidate struct {
		TaskID      int     `db:"task_id"`
		TemplateID  int     `db:"template_id"`
		InventoryID int     `db:"inventory_id"`
		Snapshot    *string `db:"execution_snapshot"`
	}
	limit := summaryPageSize(count)
	for len(items) < limit {
		query := `select t.id as task_id, t.template_id, t.execution_snapshot,
			coalesce((select max(s.inventory_id) from inventory__snapshot s where s.task_id=t.id and s.project_id=t.project_id), 0) as inventory_id
			from task t where t.project_id=?
			and exists (select 1 from task__summary_event e where e.task_id=t.id and e.project_id=t.project_id
			and e.host=? and e.kind in ('host_summary', 'task_result'))`
		args := []any{projectID, host}
		if beforeID > 0 {
			query += " and t.id<?"
			args = append(args, beforeID)
		}
		query += " order by t.id desc limit 100"
		rows := []candidate{}
		if _, err := d.connection.SelectAll(&rows, query, args...); err != nil {
			return nil, err
		}
		for _, row := range rows {
			beforeID = row.TaskID
			resolvedID := row.InventoryID
			if resolvedID == 0 && row.Snapshot != nil {
				snapshot, err := db.DecodeTaskExecutionSnapshot(*row.Snapshot, projectID, row.TemplateID)
				if err == nil && snapshot != nil && snapshot.Inventory != nil && snapshot.Inventory.ProjectID == projectID {
					resolvedID = snapshot.Inventory.ID
				}
			}
			if resolvedID == inventoryID {
				items = append(items, row.TaskID)
			}
			if len(items) == limit {
				break
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	return items, nil
}
