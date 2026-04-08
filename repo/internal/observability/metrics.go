package observability

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Metrics holds in-memory atomic counters for key application events.
type Metrics struct {
	RequestsTotal  atomic.Int64
	RequestErrors  atomic.Int64
	OrdersCreated  atomic.Int64
	OrdersCanceled atomic.Int64
	LoginSuccess   atomic.Int64
	LoginFailures  atomic.Int64
	ActiveSessions atomic.Int64 // approximate
	StartTime      time.Time
}

// M is the global Metrics instance, initialized at program start.
var M = &Metrics{StartTime: time.Now()}

// ToJSON returns a JSON-marshallable map of the current counter values.
func (m *Metrics) ToJSON() map[string]any {
	return map[string]any{
		"requests_total":  m.RequestsTotal.Load(),
		"request_errors":  m.RequestErrors.Load(),
		"orders_created":  m.OrdersCreated.Load(),
		"orders_canceled": m.OrdersCanceled.Load(),
		"login_success":   m.LoginSuccess.Load(),
		"login_failures":  m.LoginFailures.Load(),
		"active_sessions": m.ActiveSessions.Load(),
		"uptime":          m.Uptime(),
		"start_time":      m.StartTime.UTC().Format(time.RFC3339),
	}
}

// Uptime returns a human-readable string showing time elapsed since StartTime.
func (m *Metrics) Uptime() string {
	d := time.Since(m.StartTime).Truncate(time.Second)
	h := int(d.Hours())
	min := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh%dm%ds", h, min, sec)
}
