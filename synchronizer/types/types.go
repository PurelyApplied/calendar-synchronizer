package types

import (
	"context"

	"google.golang.org/api/calendar/v3"
)

type CalendarOperation string

const (
	// InsertCalendarOp - Proposed does have corresponding Existing
	InsertCalendarOp CalendarOperation = "Insert"
	// UpdateCalendarOp - Proposed has corresponding Existing, but comparator identifies are changed.
	UpdateCalendarOp CalendarOperation = "Update"
	// DeleteCalendarOp - Existing has no corresponding Proposed
	DeleteCalendarOp CalendarOperation = "Delete"
	// NilCalendarOp - Proposed events have corresponding Existing event that does not need to be updated.  No-op.
	NilCalendarOp CalendarOperation = "No-op"
)

// Eventable is the interface for proposable events.
type Eventable interface {
	// CalendarEvent returns the event as one ready to be inserted into Google Calendar.
	// TODO: For implementations of this interface, a zero-value struct must return nil.
	CalendarEvent() *calendar.Event
	// Key returns an identifiable key for the conceptual event that this proposed event represents.
	Key() string

	// Matches receives a key-matching existing Calendar event, returning true if no meaningful fields are update.
	// If true, no calendar operation will be performed.
	// If false, an Update operation will be executed
	Matches(*calendar.Event) bool
}

type Synchronizer[T Eventable] interface {
	Do(ctx context.Context, events []T) (map[string]EventPlan[T], error)
	ExecutePlan(actionPlan map[string]EventPlan[T]) error
	ActionPlan(events []T) (map[string]EventPlan[T], error)
}

// EventPlan correlates proposed and existing events.
// When the operation is performed, fields Done, ResultErr, and Existing will be populated / updated
type EventPlan[T Eventable] struct {
	Proposed T
	// TODO: Before and After operation are sharing Existing.  Should those be split?  Is there utility in having information the Before in the table?
	Existing  *calendar.Event
	Operation CalendarOperation
	Done      bool
	ResultErr error

	doCalendarOp func() (*calendar.Event, error)
}
