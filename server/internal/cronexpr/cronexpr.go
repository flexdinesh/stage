// Package cronexpr parses schedule cron expressions (standard 5-field or
// @hourly-style descriptors) evaluated in an IANA zone (docs/api.md
// "Schedules").
package cronexpr

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Spec is a parsed cron expression bound to a zone.
type Spec struct {
	sched cron.Schedule
	loc   *time.Location
}

// Parse validates expr and tz. Rejects @every and CRON_TZ=/TZ= prefixes (the
// zone is the schedule's tz).
func Parse(expr, tz string) (Spec, error) {
	loc, err := Zone(tz)
	if err != nil {
		return Spec{}, err
	}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return Spec{}, errors.New("cron is required")
	}
	if strings.HasPrefix(expr, "@every") {
		return Spec{}, errors.New("cron: @every is not supported")
	}
	if strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(expr, "TZ=") {
		return Spec{}, errors.New("cron: set the zone with tz, not a CRON_TZ=/TZ= prefix")
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return Spec{}, fmt.Errorf("cron: %w", err)
	}
	if sched.Next(time.Now().In(loc)).IsZero() {
		return Spec{}, errors.New("cron never fires")
	}
	return Spec{sched: sched, loc: loc}, nil
}

// Zone loads an IANA zone; empty and "Local" are rejected.
func Zone(tz string) (*time.Location, error) {
	if tz == "" || tz == "Local" {
		return nil, fmt.Errorf("invalid tz %q (want an IANA zone, e.g. Asia/Kolkata)", tz)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid tz %q (want an IANA zone, e.g. Asia/Kolkata)", tz)
	}
	return loc, nil
}

// Next is the first fire strictly after t (UTC). Zero when none within five
// years.
func (s Spec) Next(t time.Time) time.Time {
	n := s.sched.Next(t.In(s.loc))
	if n.IsZero() {
		return n
	}
	return n.UTC()
}
