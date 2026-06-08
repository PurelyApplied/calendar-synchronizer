// Package tabular provides a Synchronizer that uses "github.com/jedib0t/go-pretty/v6/table" to display operation
// status, etc.
package tabular

import (
	"context"
	"fmt"
	"time"

	"github.com/PurelyApplied/calendar-synchronizer/synchronizer"
	"github.com/PurelyApplied/calendar-synchronizer/synchronizer/internal"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"google.golang.org/api/calendar/v3"
)

// Eventable are synchronizer.Eventable types that also implement Row()
type Eventable interface {
	synchronizer.Eventable
	Row() table.Row
}

// test assertion: tabular Synchronizer is still a Synchronizer
var _ Synchronizer[Eventable] = &syncher[Eventable]{}

type Synchronizer[T Eventable] interface {
	synchronizer.Synchronizer[T]
	Table(plan map[string]synchronizer.EventPlan[T]) (table.Writer, TableConfig)
}

type syncher[T Eventable] struct {
	synchronizer internal.Syncher[T]
	// Header items for rows produced by each Eventable row.
	// The table returned by Table will include this Header for each Eventable entry's columns.
	header table.Row
}

func (s *syncher[T]) Do(ctx context.Context, events []T) (map[string]synchronizer.EventPlan[T], error) {
	return s.synchronizer.Do(ctx, events)
}

func (s *syncher[T]) ExecutePlan(actionPlan map[string]synchronizer.EventPlan[T]) error {
	return s.synchronizer.ExecutePlan(actionPlan)
}

func (s *syncher[T]) ActionPlan(events []T) (map[string]synchronizer.EventPlan[T], error) {
	return s.synchronizer.ActionPlan(events)
}

func New[T Eventable](Service *calendar.Service, CalendarID string, EventKey func(*calendar.Event) (string, error), Header table.Row) Synchronizer[T] {
	return &syncher[T]{
		synchronizer: internal.Syncher[T]{
			Service:    Service,
			CalendarID: CalendarID,
			EventKey:   EventKey,
		},
		header: Header,
	}
}

var RowColors = map[synchronizer.CalendarOperation]text.Color{
	synchronizer.InsertCalendarOp: text.FgBlue,
	synchronizer.DeleteCalendarOp: text.FgRed,
	synchronizer.UpdateCalendarOp: text.FgYellow,
	synchronizer.NilCalendarOp:    text.FgGreen,
}

type TableConfig struct {
	RowPainter   func(row table.Row) text.Colors
	FilterBy     []table.FilterBy
	SortBy       []table.SortBy
	ColumnConfig []table.ColumnConfig
}

// Table returns a table with the following columns:
// - "Operation" [synchronizer.CalendarOperation]
// - "Done" (bool indicating the operation was executed)
// - "Error" (error from operation execution, if any)
// - "Columns" provided by Synchronizer.Header
// - "Key" as used by the action plan
// - "Calendar Link" - the link to the Calendar event *as returned by the calendar operation* (nil for Delete operations)
// - "datetime" - A column containing time.Time representations of the Proposed event start (or Existing if no Proposed).  Note below: this column is hidden but is useful for sorting.
//
// Additionally, configures the following opinionated display options:
// - SetRowPainter based on operation actions - refer to RowColors.
// - No-op operations are filtered out - use [table.Table].FilterBy to remove filter or provide your own.
// - Rows are sorted by event start.
// - Apply column configurations such that:
// - - "datetime" column is hidden.
// - - Nil "Error" entries are printed as empty string rather than "<nil>" (to permit empty column drop).
// - - "Done" renders bools as 🗸 or
// - SuppressEmptyColumns() is given (since ideally, "Error" will be empty).
// These configurations are returned as the second return, if you would like to modify them.
func (s *syncher[T]) Table(plan map[string]synchronizer.EventPlan[T]) (table.Writer, TableConfig) {
	tbl := table.NewWriter()
	cfg := TableConfig{}
	var head table.Row
	// operationColumn determines coloring below.
	operationColumn := 0
	head = append(head, "Operation", "Done", "Error")
	head = append(head, s.header...)
	head = append(head, "Key", "Calendar link", "datetime")
	tbl.AppendHeader(head)

	for k, p := range plan {
		var row table.Row
		row = append(row, p.Operation, p.Done, p.ResultErr)
		row = append(row, p.Proposed.Row()...)
		ev := p.Proposed.CalendarEvent()
		if ev == nil {
			ev = p.Existing
		}
		startTime, err := Start(ev)
		// TODO
		_ = err
		row = append(row, k, p.Existing, startTime)
		tbl.AppendRow(row)
	}

	painter := func(row table.Row) text.Colors {
		op, ok := row[operationColumn].(synchronizer.CalendarOperation)
		if !ok {
			return nil
		}
		if c, ok := RowColors[op]; ok {
			return text.Colors{c}
		}
		return nil
	}
	tbl.SetRowPainter(painter)
	cfg.RowPainter = painter

	filter := []table.FilterBy{
		{
			Name:     "Op",
			Operator: table.NotEqual,
			Value:    synchronizer.NilCalendarOp,
		},
	}
	tbl.FilterBy(filter)
	cfg.FilterBy = filter

	sortBy := []table.SortBy{
		{
			Name: "datetime",
			Mode: table.Asc,
		},
	}
	tbl.SortBy(sortBy)
	cfg.SortBy = sortBy

	configs := []table.ColumnConfig{
		{
			Name:   "datetime",
			Hidden: true,
		},
		{
			Name: "Error",
			Transformer: func(val interface{}) string {
				if val == nil {
					return ""
				}
				return fmt.Sprintf("%v", val)
			},
		},
		{
			Name:  "Done",
			Align: text.AlignCenter,
			Transformer: func(val interface{}) string {
				// ✓✔ ✗✘
				switch d, ok := val.(bool); {
				case !ok:
					return "error: non-bool `Done` value"
				case d:
					return "✔"
				case !d:
					return "✘"
				default:
					panic("impossible")
				}
			},
		},
	}
	tbl.SetColumnConfigs(configs)
	cfg.ColumnConfig = configs

	tbl.SuppressEmptyColumns()
	return tbl, cfg
}

// EventPlan correlates proposed and existing events, noting intended Calendar operations and holding any error experienced.
type EventPlan[T Eventable] struct {
	synchronizer.EventPlan[T]
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

	row = append(row, ec.Row())

	url := ""
	if ec.Existing != nil {
		url = ec.Existing.HtmlLink
	}
	row = append(row, url)

	return row
}

func Start(e *calendar.Event) (time.Time, error) {
	switch {
	case e.Start.DateTime != "":
		return time.Parse(time.RFC3339, e.Start.DateTime)
	case e.Start.Date != "":
		return time.Parse(time.DateOnly, e.Start.Date)
	default:
		return time.Time{}, fmt.Errorf("event has neither Start.Date nor Start.DateTime; e=%#v", e)
	}
}
