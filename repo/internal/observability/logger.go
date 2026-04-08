package observability

import (
	"io"
	"log/slog"
	"os"
)

// Global loggers per category. All are safe to use before Init is called;
// they are initialized to a no-op-safe default on package load.
var (
	HTTP         *slog.Logger
	Auth         *slog.Logger
	Orders       *slog.Logger
	Distribution *slog.Logger
	Moderation   *slog.Logger
	Scheduler    *slog.Logger
	DB           *slog.Logger
	Security     *slog.Logger
	App          *slog.Logger // general
)

func init() {
	// Pre-initialize with a default text handler so log calls never panic
	// even if Init has not been called.
	initLoggers("development", os.Stderr)
}

// Init sets up all loggers. It is safe to call multiple times (idempotent —
// each call reinitializes the loggers).
//
//   - In production (APP_ENV=production): JSON handler, INFO level
//   - In development: Text handler, DEBUG level
func Init(appEnv string) {
	initLoggers(appEnv, os.Stderr)
}

// InitWithWriter is like Init but writes to w. Useful for tests.
func InitWithWriter(appEnv string, w io.Writer) {
	initLoggers(appEnv, w)
}

func initLoggers(appEnv string, w io.Writer) {
	var level slog.Level
	var handler slog.Handler

	if appEnv == "production" {
		level = slog.LevelInfo
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	} else {
		level = slog.LevelDebug
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	}

	newCategoryLogger := func(category string) *slog.Logger {
		return slog.New(handler).With(slog.String("category", category))
	}

	HTTP = newCategoryLogger("http")
	Auth = newCategoryLogger("auth")
	Orders = newCategoryLogger("orders")
	Distribution = newCategoryLogger("distribution")
	Moderation = newCategoryLogger("moderation")
	Scheduler = newCategoryLogger("scheduler")
	DB = newCategoryLogger("db")
	Security = newCategoryLogger("security")
	App = newCategoryLogger("app")
}
