package synchronizer

import (
	"github.com/PurelyApplied/calendar-synchronizer/synchronizer/internal"
	"github.com/PurelyApplied/calendar-synchronizer/synchronizer/types"
	"google.golang.org/api/calendar/v3"
)

func New[T types.Eventable](Service *calendar.Service,
	CalendarID string,
	EventKey func(*calendar.Event) (string, error),
) types.Synchronizer[T] {
	return &internal.Syncher[T]{
		Service:    Service,
		CalendarID: CalendarID,
		EventKey:   EventKey,
	}
}
