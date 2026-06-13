package internal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/PurelyApplied/calendar-synchronizer/synchronizer/types"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
)

// test assertion: tabular Synchronizer is still a Synchronizer
var _ types.Synchronizer[types.Eventable] = &Syncher[types.Eventable]{}

type Syncher[T types.Eventable] struct {
	Service    *calendar.Service
	CalendarID string

	// EventKey returns the identifiable key for the conceptual event that this calendar event represents.
	EventKey func(*calendar.Event) (string, error)
}

// Do plans and executes the necessary Calendar operations to sync events.
func (s *Syncher[T]) Do(ctx context.Context, events []T) (map[string]types.EventPlan[T], error) {
	plan, err := s.ActionPlan(events)
	if err != nil {
		return plan, err
	}

	return plan, s.ExecutePlan(plan)
}

// ExecutePlan executes the plan produced by Syncher.ActionPlan.  That method is exposed for logging/printing purposes.
// If no logging is desired, simply call Syncher.Do instead.
func (s *Syncher[T]) ExecutePlan(actionPlan map[string]types.EventPlan[T]) error {
	for k, plan := range actionPlan {
		op := strings.ToUpper(string(plan.Operation))
		slog.Debug(fmt.Sprintf("%s calendar event", op), "proposed", plan.Proposed, "proposed event", plan.Proposed.CalendarEvent(), "existing", plan.Existing)

		ev, err := s.execute(plan)
		plan.Existing = ev
		plan.ResultErr = err
		plan.Done = true
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

// ActionPlan produces a plan for Calendar synchronization.
// The returned collection may be useful for printing etc.
// If not required, call Syncher.Do instead.
func (s *Syncher[T]) ActionPlan(events []T) (map[string]types.EventPlan[T], error) {
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

	plans := make(map[string]types.EventPlan[T])
	for _, event := range calendarEvents {
		key, err := s.EventKey(event)
		if err != nil {
			return nil, err
		}

		plans[key] = types.EventPlan[T]{
			Existing: event,
		}
	}
	for _, proposed := range events {
		plans[proposed.Key()] = types.EventPlan[T]{
			Existing: plans[proposed.Key()].Existing,
			Proposed: proposed,
		}
	}

	for k, plan := range plans {
		switch {
		case plan.Proposed.CalendarEvent() != nil && plan.Existing == nil:
			plan.Operation = types.InsertCalendarOp
			plans[k] = plan
		case plan.Proposed.CalendarEvent() == nil && plan.Existing != nil:
			plan.Operation = types.DeleteCalendarOp
			plans[k] = plan
		case plan.Proposed.CalendarEvent() != nil && plan.Existing != nil && plan.Proposed.Matches(plan.Existing):
			plan.Operation = types.NilCalendarOp
			plans[k] = plan
		case plan.Proposed.CalendarEvent() != nil && plan.Existing != nil && !plan.Proposed.Matches(plan.Existing):
			plan.Operation = types.UpdateCalendarOp
			plans[k] = plan
		default:
			panic("Both proposed and existing events are nil, but somehow we still have this event keyed.")
		}
	}

	return plans, nil
}

func (s *Syncher[T]) calendarQueryTimeMin(events []T) time.Time {
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

func (s *Syncher[T]) execute(plan types.EventPlan[T]) (*calendar.Event, error) {
	switch op := plan.Operation; op {
	case types.InsertCalendarOp:
		return s.Service.Events.Insert(s.CalendarID, plan.Proposed.CalendarEvent()).Do()
	case types.DeleteCalendarOp:
		// Return the old event link for data tracking.  Even though deleted, the link could be useful as a reference, etc.
		return plan.Existing, s.Service.Events.Delete(s.CalendarID, plan.Existing.Id).Do()
	case types.NilCalendarOp:
		return plan.Existing, nil
	case types.UpdateCalendarOp:
		return s.Service.Events.Update(s.CalendarID, plan.Existing.Id, plan.Proposed.CalendarEvent()).Do()
	default:
		return nil, fmt.Errorf("unexpected operation %q", op)
	}
}
