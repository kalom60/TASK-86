package observability_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"w2t86/internal/observability"
)

// TestInit_Development verifies that calling Init with "development" leaves all
// category loggers non-nil.
func TestInit_Development(t *testing.T) {
	observability.InitWithWriter("development", io.Discard)

	if observability.HTTP == nil {
		t.Error("HTTP logger is nil after Init(development)")
	}
	if observability.Auth == nil {
		t.Error("Auth logger is nil after Init(development)")
	}
	if observability.Orders == nil {
		t.Error("Orders logger is nil after Init(development)")
	}
	if observability.Distribution == nil {
		t.Error("Distribution logger is nil after Init(development)")
	}
	if observability.Moderation == nil {
		t.Error("Moderation logger is nil after Init(development)")
	}
	if observability.Scheduler == nil {
		t.Error("Scheduler logger is nil after Init(development)")
	}
	if observability.DB == nil {
		t.Error("DB logger is nil after Init(development)")
	}
	if observability.Security == nil {
		t.Error("Security logger is nil after Init(development)")
	}
	if observability.App == nil {
		t.Error("App logger is nil after Init(development)")
	}
}

// TestInit_Production verifies that calling Init with "production" leaves all
// category loggers non-nil.
func TestInit_Production(t *testing.T) {
	observability.InitWithWriter("production", io.Discard)

	if observability.HTTP == nil {
		t.Error("HTTP logger is nil after Init(production)")
	}
	if observability.Auth == nil {
		t.Error("Auth logger is nil after Init(production)")
	}
}

// TestRequestID_SetsHeader verifies that RequestID middleware sets the
// X-Request-ID response header.
func TestRequestID_SetsHeader(t *testing.T) {
	app := fiber.New()
	app.Use(observability.RequestID())
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	id := resp.Header.Get("X-Request-ID")
	if id == "" {
		t.Error("X-Request-ID header not set")
	}
}

// TestRequestID_PreservesExisting verifies that a pre-existing X-Request-ID
// header is propagated rather than replaced.
func TestRequestID_PreservesExisting(t *testing.T) {
	app := fiber.New()
	app.Use(observability.RequestID())
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "fixed-id-123")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	id := resp.Header.Get("X-Request-ID")
	if id != "fixed-id-123" {
		t.Errorf("expected X-Request-ID=fixed-id-123, got %q", id)
	}
}

// TestRequestLogger_LogsRequest verifies that RequestLogger does not panic and
// returns the underlying handler's status code correctly.
func TestRequestLogger_LogsRequest(t *testing.T) {
	observability.InitWithWriter("development", io.Discard)

	app := fiber.New()
	app.Use(observability.RequestID())
	app.Use(observability.RequestLogger())
	app.Get("/ping", func(c *fiber.Ctx) error {
		return c.Status(200).SendString("pong")
	})

	req := httptest.NewRequest("GET", "/ping", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// TestRequestLogger_SkipsHealth verifies that /health requests do not produce
// log output (the middleware returns without logging).
func TestRequestLogger_SkipsHealth(t *testing.T) {
	var buf bytes.Buffer
	observability.InitWithWriter("development", &buf)

	app := fiber.New()
	app.Use(observability.RequestLogger())
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	if strings.Contains(buf.String(), "/health") {
		t.Error("expected /health request not to be logged")
	}
}

// TestMetrics_Increment_And_ToJSON verifies that atomic counter increments are
// reflected in ToJSON output.
func TestMetrics_Increment_And_ToJSON(t *testing.T) {
	// Use a fresh Metrics instance so tests are isolated.
	m := &observability.Metrics{}
	m.RequestsTotal.Add(5)
	m.RequestErrors.Add(2)
	m.OrdersCreated.Add(3)
	m.OrdersCanceled.Add(1)
	m.LoginSuccess.Add(10)
	m.LoginFailures.Add(4)

	data := m.ToJSON()

	checkInt := func(key string, want int64) {
		t.Helper()
		v, ok := data[key]
		if !ok {
			t.Errorf("key %q missing from ToJSON", key)
			return
		}
		// json.Marshal round-trip converts numbers to float64.
		raw, _ := json.Marshal(v)
		var got float64
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("key %q: cannot unmarshal %v: %v", key, v, err)
			return
		}
		if int64(got) != want {
			t.Errorf("key %q: want %d, got %d", key, want, int64(got))
		}
	}

	checkInt("requests_total", 5)
	checkInt("request_errors", 2)
	checkInt("orders_created", 3)
	checkInt("orders_canceled", 1)
	checkInt("login_success", 10)
	checkInt("login_failures", 4)
}

// TestMetrics_Uptime_NonEmpty verifies that Uptime returns a non-empty string.
func TestMetrics_Uptime_NonEmpty(t *testing.T) {
	m := &observability.Metrics{}
	// StartTime is zero value, so uptime will be very large — just check non-empty.
	u := m.Uptime()
	if u == "" {
		t.Error("Uptime returned empty string")
	}
}
