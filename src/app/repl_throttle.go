package main

// Bandwidth throttling for replication. A schedule gives one limit for
// business hours and another for the rest of the week, so a job can trickle
// during the working day and run at full speed at night. Limits are in
// megabits per second, the unit network teams use; 0 means no limit.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type vmThrottleSchedule struct {
	// LimitMbps applies outside business hours (or always when no business
	// hours are set).
	LimitMbps int `json:"limitMbps"`
	// BusinessMbps applies during business hours; 0 there means no limit.
	BusinessMbps int `json:"businessMbps"`
	// BusinessStart and BusinessEnd are hours of the day (0-24) in the
	// source computer's local time. Equal values switch business hours off.
	BusinessStart int `json:"businessStart"`
	BusinessEnd   int `json:"businessEnd"`
	// BusinessDays are weekdays (0 Sunday to 6 Saturday).
	BusinessDays []int `json:"businessDays,omitempty"`
}

func (s vmThrottleSchedule) Validate() error {
	if s.LimitMbps < 0 || s.BusinessMbps < 0 || s.LimitMbps > 100000 || s.BusinessMbps > 100000 {
		return errors.New("bandwidth limits must be from 0 (no limit) to 100000 Mbps")
	}
	if s.BusinessStart < 0 || s.BusinessStart > 24 || s.BusinessEnd < 0 || s.BusinessEnd > 24 {
		return errors.New("business hours must be from 0 to 24")
	}
	for _, day := range s.BusinessDays {
		if day < 0 || day > 6 {
			return errors.New("business days must be from 0 (Sunday) to 6 (Saturday)")
		}
	}
	return nil
}

// Business says whether now falls in business hours.
func (s vmThrottleSchedule) Business(now time.Time) bool {
	if s.BusinessStart == s.BusinessEnd {
		return false
	}
	days := s.BusinessDays
	if len(days) == 0 {
		days = []int{1, 2, 3, 4, 5}
	}
	today := false
	for _, day := range days {
		if int(now.Weekday()) == day {
			today = true
		}
	}
	if !today {
		return false
	}
	hour := now.Hour()
	if s.BusinessStart < s.BusinessEnd {
		return hour >= s.BusinessStart && hour < s.BusinessEnd
	}
	return hour >= s.BusinessStart || hour < s.BusinessEnd
}

// BytesPerSecond is the limit in force at now; 0 means none.
func (s vmThrottleSchedule) BytesPerSecond(now time.Time) int64 {
	mbps := s.LimitMbps
	if s.Business(now) {
		mbps = s.BusinessMbps
	}
	return int64(mbps) * 1000 * 1000 / 8
}

func (s vmThrottleSchedule) Describe() string {
	describe := func(mbps int) string {
		if mbps == 0 {
			return "no limit"
		}
		return fmt.Sprintf("%d Mbps", mbps)
	}
	if s.BusinessStart == s.BusinessEnd {
		return describe(s.LimitMbps)
	}
	return fmt.Sprintf("%s from %02d:00 to %02d:00 on business days, %s otherwise", describe(s.BusinessMbps), s.BusinessStart, s.BusinessEnd, describe(s.LimitMbps))
}

// vmThrottle paces bytes to the schedule's current limit. One throttle can
// be shared by every disk of a job so the limit covers the whole job.
type vmThrottle struct {
	mu       sync.Mutex
	schedule vmThrottleSchedule
	// next is when the bytes already allowed have been sent at the
	// limit; it never lags now by more than one second (the burst).
	next  time.Time
	now   func() time.Time
	until func(context.Context, time.Time) error
}

// vmThrottleBurst is how much sending may run ahead of the limit.
const vmThrottleBurst = time.Second

func newVMThrottle(schedule vmThrottleSchedule) *vmThrottle {
	return &vmThrottle{schedule: schedule, now: time.Now, until: func(ctx context.Context, deadline time.Time) error {
		return vmSleep(ctx, time.Until(deadline))
	}}
}

func vmSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// SetSchedule changes the limits of a running job.
func (t *vmThrottle) SetSchedule(schedule vmThrottleSchedule) {
	t.mu.Lock()
	t.schedule = schedule
	t.mu.Unlock()
}

// Wait blocks until n more bytes may be sent. Callers sending at once
// each reserve their bytes in turn, so they wait one after another.
func (t *vmThrottle) Wait(ctx context.Context, n int64) error {
	if t == nil || n <= 0 {
		return nil
	}
	t.mu.Lock()
	now := t.now()
	limit := t.schedule.BytesPerSecond(now)
	if limit <= 0 {
		t.next = time.Time{}
		t.mu.Unlock()
		return nil
	}
	if earliest := now.Add(-vmThrottleBurst); t.next.Before(earliest) {
		t.next = earliest
	}
	t.next = t.next.Add(time.Duration(float64(n) / float64(limit) * float64(time.Second)))
	deadline := t.next
	t.mu.Unlock()
	if !deadline.After(now) {
		return nil
	}
	return t.until(ctx, deadline)
}
