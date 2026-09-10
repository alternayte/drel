package testmodels

import (
	"github.com/alternayte/drel"
)

type EventUser struct {
	drel.Model[int]
	name  string
	email string
}

func NewEventUser(name, email string) *EventUser {
	return &EventUser{name: name, email: email}
}

func (u *EventUser) Name() string  { return u.name }
func (u *EventUser) Email() string { return u.email }

type eventUserSnapshot struct {
	name  string
	email string
}

var EventUserMeta = drel.ModelMeta[EventUser]{
	Table:     "event_users",
	Columns:   []string{"id", "name", "email", "created_at", "updated_at"},
	PKColumns: []string{"id"},
	Scan: func(row drel.Row) (*EventUser, error) {
		u := &EventUser{}
		idPtr, createdAtPtr, updatedAtPtr := u.ScanPtrs()
		err := row.Scan(idPtr, &u.name, &u.email, createdAtPtr, updatedAtPtr)
		if err != nil {
			return nil, err
		}
		return u, nil
	},
	Snapshot: func(u *EventUser) any {
		return eventUserSnapshot{name: u.name, email: u.email}
	},
	Diff: func(u *EventUser, snap any) []drel.FieldChange {
		s := snap.(eventUserSnapshot)
		var changes []drel.FieldChange
		if u.name != s.name {
			changes = append(changes, drel.FieldChange{Column: "name", Value: u.name})
		}
		if u.email != s.email {
			changes = append(changes, drel.FieldChange{Column: "email", Value: u.email})
		}
		return changes
	},
	PKValue: func(u *EventUser) any {
		return u.ID()
	},
	InsertColumns: func(u *EventUser) ([]string, []any) {
		return []string{"name", "email"}, []any{u.name, u.email}
	},
	ScanReturning: func(u *EventUser, row drel.Row) error {
		idPtr, createdAtPtr, updatedAtPtr := u.ScanPtrs()
		return row.Scan(idPtr, createdAtPtr, updatedAtPtr)
	},
}
