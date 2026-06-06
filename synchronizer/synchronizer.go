package synchronizer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
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

type Tabularizable interface {
	Row() table.Row
}

type Synchronizer[T Eventable] struct {
	Service    *calendar.Service
	CalendarID string

	// EventKey returns the identifiable key for the conceptual event that this calendar event represents.
	EventKey func(*calendar.Event) (string, error)
}

// EventPlan correlates proposed and existing events, noting intended Calendar operations and holding any error experienced.
type EventPlan[T Eventable] struct {
	Proposed  T
	Existing  *calendar.Event
	Operation CalendarOperation
	Done      bool
	ResultErr error

	doCalendarOp func() (*calendar.Event, error)
}

func (ec *EventPlan[T]) executeAndRecord() error {
	ev, err := ec.doCalendarOp()
	ec.Existing = ev
	ec.ResultErr = err
	ec.Done = true

	return err
}

// Row returns a table.Row containing State, ResultErr, Proposed event, and Calendar Event URL (if any).
// If the proposed event is Tabularizable, .Row() is called to expand the columns; otherwise it is passed directly.
func (ec *EventPlan[T]) Row() table.Row {
	row := table.Row{}
	row = append(row, ec.Operation)

	var result string
	if ec.ResultErr != nil {
		result = ec.ResultErr.Error()
	}
	row = append(row, result)

	if c, ok := any(ec.Proposed).(Tabularizable); ok {
		row = append(row, c.Row()...)
	} else {
		row = append(row, ec.Proposed)
	}

	url := ""
	if ec.Existing != nil {
		url = ec.Existing.HtmlLink
	}
	row = append(row, url)

	return row
}

// Do plans and executes the necessary Calendar operations to sync events.
func (s *Synchronizer[T]) Do(ctx context.Context, events []T) (map[string]EventPlan[T], error) {
	plan, err := s.ActionPlan(events)
	if err != nil {
		return plan, err
	}

	return plan, s.ExecutePlan(plan)
}

// ExecutePlan executes the plan produced by Synchronizer.ActionPlan.  That method is exposed for logging/printing purposes.
// If no logging is desired, simply call Synchronizer.Do instead.
func (s *Synchronizer[T]) ExecutePlan(actionPlan map[string]EventPlan[T]) error {
	for k, plan := range actionPlan {
		op := strings.ToUpper(string(plan.Operation))
		slog.Debug(fmt.Sprintf("%s calendar event", op), "proposed", plan.Proposed, "existing", plan.Existing)

		err := plan.executeAndRecord()
		if err != nil {
			slog.Warn(fmt.Sprintf("%s calendar event failed", op), "error", err)
		}

		actionPlan[k] = plan
	}

	var allErrors []error
	for _, plan := range actionPlan {
		allErrors = append(allErrors, plan.ResultErr)
	}
	return errors.Join(allErrors...)
}

// TODO: Opts - Aggregate errs or eject

// ActionPlan produces a plan for Calendar synchronization.
// The returned collection may be useful for printing etc.
// If not required, call Synchronizer.Do instead.
func (s *Synchronizer[T]) ActionPlan(events []T) (map[string]EventPlan[T], error) {
	// TODO: Hypothetical OOM risk just dumping all pages into a slice,
	// but in local work, this is using less memory than Firefox.
	// TODO: Could be mitigated a bit by the Fields() option, but that doesn't seem to like the parameters with their given names?
	// TODO: Pass that as a query opt, especially if it's going to be used by callers in EventKey.
	var calendarEvents []*calendar.Event

	nextToken := "start"
	initQuery := s.Service.Events.List(s.CalendarID).TimeMin(s.calendarQueryTimeMin(events).Format(time.RFC3339)).ShowDeleted(false)
	nextPage := func(nextToken string) func(...googleapi.CallOption) (*calendar.Events, error) {
		return s.Service.Events.List(s.CalendarID).PageToken(nextToken).ShowDeleted(false).Do
	}

	for eventsResp, err := initQuery.Do(); nextToken != ""; eventsResp, err = nextPage(nextToken)() {
		if err != nil {
			return nil, fmt.Errorf("Events.List: %w", err)
		}
		calendarEvents = append(calendarEvents, eventsResp.Items...)
		nextToken = eventsResp.NextPageToken
	}

	plans := make(map[string]EventPlan[T])
	for _, event := range calendarEvents {
		key, err := s.EventKey(event)
		if err != nil {
			return nil, err
		}

		plans[key] = EventPlan[T]{
			Existing: event,
		}
	}
	for _, proposed := range events {
		plans[proposed.Key()] = EventPlan[T]{
			Existing: plans[proposed.Key()].Existing,
			Proposed: proposed,
		}
	}

	for k, plan := range plans {
		switch {
		case plan.Proposed.CalendarEvent() != nil && plan.Existing == nil:
			plan.Operation = InsertCalendarOp
			plan.doCalendarOp = func() (*calendar.Event, error) {
				return s.Service.Events.Insert(s.CalendarID, plan.Proposed.CalendarEvent()).Do()
			}
			plans[k] = plan
		case plan.Proposed.CalendarEvent() == nil && plan.Existing != nil:
			plan.Operation = DeleteCalendarOp
			plan.doCalendarOp = func() (*calendar.Event, error) {
				// Return the old event link for data tracking.  Even though deleted, the link could be useful as a reference, etc.
				return plan.Existing, s.Service.Events.Delete(s.CalendarID, plan.Existing.Id).Do()
			}
			plans[k] = plan
		case plan.Proposed.CalendarEvent() != nil && plan.Existing != nil && plan.Proposed.Matches(plan.Existing):
			plan.Operation = NilCalendarOp
			plan.doCalendarOp = func() (*calendar.Event, error) {
				return plan.Existing, nil
			}
			plans[k] = plan
		case plan.Proposed.CalendarEvent() != nil && plan.Existing != nil && !plan.Proposed.Matches(plan.Existing):
			plan.Operation = UpdateCalendarOp
			plan.doCalendarOp = func() (*calendar.Event, error) {
				return s.Service.Events.Update(s.CalendarID, plan.Existing.Id, plan.Proposed.CalendarEvent()).Do()
			}
			plans[k] = plan
		default:
			panic("Both proposed and existing events are nil, but somehow we still have this event keyed.")
		}
	}

	return plans, nil
}

func (s *Synchronizer[T]) calendarQueryTimeMin(events []T) time.Time {
	timeMin := time.Now()
	for _, ev := range events {
		e := ev.CalendarEvent()
		var start time.Time
		if e.Start.DateTime != "" {
			var err error
			start, err = time.Parse(time.RFC3339, e.Start.DateTime)
			if err != nil {
				slog.Warn("Proposed event does not have RFC3339 formated datetime, making it invalid for insertion.  Skipping in query time min, but this will cause a failure later.", "DateTime", e.Start.DateTime, "event", ev)
				continue
			}
		} else if e.Start.Date != "" {
			var err error
			start, err = time.Parse(time.DateOnly, e.Start.Date)
			if err != nil {
				slog.Warn("Proposed event does not have ISO 8601 formated date, making it invalid for insertion.  Skipping in query time min, but this will cause a failure later.", "Date", e.Start.Date, "event", ev)
				continue
			}
		} else {
			slog.Warn("Event does not have Start.Date nor Start.DateTime.  Did that field get filtered?")
			continue
		}

		if start.Before(timeMin) {
			timeMin = start
		}
	}
	timeMin = timeMin.Add(-time.Hour)
	return timeMin
}
