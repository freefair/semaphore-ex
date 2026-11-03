package sql

import (
	"database/sql"
	"errors"
	"github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
)

// getEvents filters before applying a response limit, fetching bounded pages
// so hidden recent events cannot force an entire history into memory.
func (d *SqlDb) getEvents(q squirrel.SelectBuilder, params db.RetrieveQueryParams) (events []db.Event, err error) {
	if !filtered(params.Viewer) {
		if params.Count > 0 {
			q = q.Limit(uint64(params.Count))
		}
		query, args, buildErr := q.ToSql()
		if buildErr != nil {
			return nil, buildErr
		}
		if _, err = d.selectAll(&events, query, args...); err != nil {
			return nil, err
		}
		err = db.FillEvents(d, events)
		return events, err
	}
	const pageSize = 200
	var before int
	for {
		pageQuery := q.Limit(pageSize)
		if before > 0 {
			pageQuery = pageQuery.Where("event.id < ?", before)
		}
		query, args, buildErr := pageQuery.ToSql()
		if buildErr != nil {
			return nil, buildErr
		}
		var page []db.Event
		if _, err = d.selectAll(&page, query, args...); err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		lastID := before
		for _, event := range page {
			if event.ID <= 0 || (lastID > 0 && event.ID >= lastID) {
				return nil, db.ErrInvalidOperation
			}
			lastID = event.ID
			if event.ObjectType != nil && *event.ObjectType == db.EventTask {
				if event.ProjectID == nil || event.ObjectID == nil {
					continue
				}
				permitted, permissionErr := d.taskVisible(*event.ProjectID, event.ObjectID, params.Viewer.UserID)
				if errors.Is(permissionErr, sql.ErrNoRows) || errors.Is(permissionErr, db.ErrNotFound) {
					continue
				}
				if permissionErr != nil {
					return nil, permissionErr
				}
				if !permitted {
					continue
				}
			}
			events = append(events, event)
			if params.Count > 0 && len(events) >= params.Count {
				break
			}
		}
		if params.Count > 0 && len(events) >= params.Count {
			break
		}
		if len(page) < pageSize {
			break
		}
		before = lastID
	}
	err = db.FillEvents(d, events)
	return events, err
}

func (d *SqlDb) CreateEvent(evt db.Event) (newEvent db.Event, err error) {
	var created = tz.Now()

	_, err = d.exec(
		"insert into event(user_id, project_id, object_id, object_type, description, created) values (?, ?, ?, ?, ?, ?)",
		evt.UserID,
		evt.ProjectID,
		evt.ObjectID,
		evt.ObjectType,
		evt.Description,
		created)

	if err != nil {
		return
	}

	newEvent = evt
	newEvent.Created = created
	return
}

func (d *SqlDb) GetUserEvents(userID int, params db.RetrieveQueryParams) ([]db.Event, error) {
	if params.Viewer == nil {
		params.Viewer = &db.Viewer{UserID: userID}
	}
	q := squirrel.Select("event.*, p.name as project_name").
		From("event").
		LeftJoin("project as p on event.project_id=p.id").
		OrderBy("id desc").
		Where("((event.project_id IS NULL AND (event.object_type IS NULL OR event.object_type != ?)) OR exists (select 1 from project__user_role pur left join `role` r on r.role_id=pur.role_id where pur.project_id=event.project_id and pur.user_id=? and (pur.role_id in ('builtin:owner','builtin:manager','builtin:task_runner','builtin:guest') or r.permissions & ? <> 0)))", db.EventProjectRunnerAudit, userID, db.CanReadProject)

	return d.getEvents(q, params)
}

func (d *SqlDb) GetEvents(projectID int, params db.RetrieveQueryParams) ([]db.Event, error) {
	q := squirrel.Select("event.*, p.name as project_name").
		From("event").
		LeftJoin("project as p on event.project_id=p.id").
		OrderBy("id desc").
		Where("event.project_id=?", projectID)

	return d.getEvents(q, params)
}

func (d *SqlDb) GetAllEvents(params db.RetrieveQueryParams) ([]db.Event, error) {
	q := squirrel.Select("event.*, p.name as project_name").
		From("event").
		LeftJoin("project as p on event.project_id=p.id").
		OrderBy("id desc")

	return d.getEvents(q, params)
}
