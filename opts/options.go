package opts

import (
	"time"

	"google.golang.org/api/googleapi"
)

type Options struct {
	CalendarQueryOptions
}

type CalendarQueryOptions struct {
	// TimeMinBuffer infers TimeMin by subtacting TimeMinBuffer duration before the minimum time seen among all proposed events.
	TimeMinBuffer time.Duration
	// TimeMin sets an explicit TimeMin for the Calendar events query, discarding TimeMinBuffer if both are set.
	TimeMin time.Time

	// TimeMaxBuffer infers TimeMax by adding TimeMaxBuffer duration before the minimum time seen among all proposed events.
	TimeMaxBuffer time.Duration
	// TimeMax sets an explicit TimeMax for the Calendar events query, discarding TimeMaxBuffer if both are set.
	TimeMax time.Time

	GoogleOpts []googleapi.CallOption
}

// New returns a new set of options with reasonable defaults.
func New() Options {
	return Options{
		CalendarQueryOptions: CalendarQueryOptions{
			TimeMinBuffer: -time.Hour,
			GoogleOpts:    nil,
		},
	}
}
