# District Materials Commerce & Logistics Portal

A web-based portal for managing district-wide distribution of educational materials. It provides role-aware workflows for students (browsing, ordering, favorites), instructors (course plans, approvals), clerks (distribution, ledger), moderators (comment queue), and administrators (users, analytics, settings). Built with Go, Fiber, SQLite, HTMX, and Alpine.js.

## Prerequisites

- Go 1.22 or later
- GCC (required to compile the `mattn/go-sqlite3` CGo driver)
  - macOS: `xcode-select --install`
  - Ubuntu/Debian: `sudo apt install gcc`
  - Windows: install [TDM-GCC](https://jmeubank.github.io/tdm-gcc/) or use WSL

## Setup

1. **Clone the repository**

   ```bash
   git clone <repo-url>
   cd w2t86
   ```

2. **Install Go dependencies**

   ```bash
   go mod tidy
   ```

3. **Frontend assets (vendored in-repo)**

   All JavaScript and CSS libraries are vendored directly into `web/static/` so
   the portal runs **fully offline** without any CDN dependency:

   | File | Library | Version |
   |---|---|---|
   | `web/static/js/htmx.min.js` | HTMX | 2.0.4 |
   | `web/static/js/alpine.min.js` | Alpine.js | 3.14.3 |
   | `web/static/js/bootstrap.bundle.min.js` | Bootstrap (JS + Popper) | 5.3.3 |
   | `web/static/js/leaflet.js` | Leaflet | 1.9.4 |
   | `web/static/js/htmx-ext-sse.js` | HTMX SSE Extension | 2.2.2 |
   | `web/static/css/bootstrap.min.css` | Bootstrap CSS | 5.3.3 |
   | `web/static/css/bootstrap-icons.min.css` | Bootstrap Icons | 1.11.3 |
   | `web/static/css/leaflet.css` | Leaflet CSS | 1.9.4 |

   If you need to refresh assets (e.g. after a security patch), run:

   ```bash
   make vendor-assets
   ```

4. **Configure environment**

   Generate the required secrets and write `.env` in one step:

   ```bash
   cat > .env << EOF
   PORT=3000
   DB_PATH=data/portal.db
   ENCRYPTION_KEY=$(openssl rand -hex 32)
   SESSION_SECRET=$(openssl rand -hex 32)
   APP_ENV=development
   TIMEZONE=UTC
   EOF
   ```

   The `.env` file is gitignored — **never commit it with real secrets**.
   `.env.example` contains only safe placeholder values and is tracked.

   See the [Environment Variables](#environment-variables) section below for a
   description of every variable.

5. **Run the server**

   ```bash
   go run -tags sqlite_fts5 ./cmd/server
   ```

   The server listens on `http://localhost:3000` by default.

## Docker

### Quick start

```bash
# 1. Generate .env with cryptographically-random secrets (portable — macOS and Linux)
cat > .env << EOF
PORT=3000
DB_PATH=/app/data/portal.db
ENCRYPTION_KEY=$(openssl rand -hex 32)
SESSION_SECRET=$(openssl rand -hex 32)
APP_ENV=production
TIMEZONE=UTC
EOF

# 2. Build and start (detached)
docker compose up -d

# 3. Retrieve the one-time admin password printed on first boot
docker compose logs portal | grep SECURITY
```

> The `temporary_password` field in that log line is the admin password.
> Log in at `http://localhost:3000` and change it immediately via Admin Settings.

### Development (live template reload)

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up
```

### Useful commands

```bash
make docker-logs   # tail logs (Ctrl-C to stop)
make docker-down   # stop and remove containers
make test          # run test suite locally (no Docker required)
```

## Running Tests (no Docker)

All tests run locally without any external services; an in-memory SQLite database is created per test.

```bash
# Run the full test suite (unit + service + repository + API + integration):
make test

# Verbose output:
make test-verbose

# Run a specific package:
go test -tags sqlite_fts5 ./internal/services/...
go test -tags sqlite_fts5 ./API_tests/...
go test -tags sqlite_fts5 ./internal/integration/...

# Run a single test by name:
go test -tags sqlite_fts5 -run TestApproveReturn ./internal/integration/...
```

> The `-tags sqlite_fts5` build tag is required throughout; it enables the full-text-search
> extension in the SQLite driver.

## Environment Variables

The application reads all configuration from environment variables.
**`.env.example`** is committed to the repository with placeholder values only —
no real secrets are stored in it.  Copy it to `.env`, replace the placeholder
values, and the file will be ignored by git (`.env` is in `.gitignore`).

### Minimal `.env` for local development

```dotenv
# --- Required secrets (generate with: openssl rand -hex 32) ---
ENCRYPTION_KEY=<64-hex-char string, 32 bytes>
SESSION_SECRET=<long random string>

# --- Optional: shown here with their default values ---
PORT=3000
DB_PATH=data/portal.db
APP_ENV=development
BANNED_WORDS=
TIMEZONE=UTC
```

### Variable reference

| Variable | Required | Default | Description |
|---|---|---|---|
| `ENCRYPTION_KEY` | **yes** | — | 64-character hex string (32 bytes) used as the AES-256-GCM key for encrypting sensitive user custom fields. Generate with `openssl rand -hex 32`. |
| `SESSION_SECRET` | **yes** | — | Arbitrary secret string used to sign and verify session tokens. Use a long random value; `openssl rand -hex 32` is sufficient. |
| `PORT` | no | `3000` | TCP port the HTTP server binds to. |
| `DB_PATH` | no | `data/portal.db` | Filesystem path to the SQLite database file. The parent directory is created automatically on first run. In Docker the volume is mounted at `/app/data`, so use `/app/data/portal.db`. |
| `APP_ENV` | no | `development` | Runtime environment. Set to `production` to disable template hot-reload and enable stricter security defaults. Any other value is treated as development. |
| `BANNED_WORDS` | no | *(empty)* | Comma-separated list of words blocked in material comments (e.g. `spam,abuse`). Leave empty to disable the filter entirely. |
| `TIMEZONE` | no | `UTC` | IANA timezone name used for Do-Not-Disturb window evaluation (e.g. `America/New_York`, `Europe/Berlin`). The value is shown to users on their notification settings page. |

## Default Credentials

The admin account is seeded with a **non-functional bootstrap placeholder** — there is no
known default password. On first boot, the server detects the placeholder, generates a
cryptographically-random password, and logs it **once** as a structured log line at the
`ERROR` level (search for `"SECURITY: admin bootstrap credential auto-rotated"`).

| Username | Password                          | Role  |
|----------|-----------------------------------|-------|
| `admin`  | *(retrieve from server log)*      | admin |

> **Procedure:** Start the server, read the `temporary_password` field from the log line,
> log in, and change the password via the Admin Settings page. The account is flagged
> `must_change_password = 1` so the first login forces an immediate password reset.

## Available Roles

| Role         | Capabilities                                                                                                           |
|--------------|------------------------------------------------------------------------------------------------------------------------|
| `student`    | Browse materials, place orders, manage favorites, inbox                                                                |
| `instructor` | Course plans, approve orders, **approve/reject return & refund requests**, inbox — _equivalent to manager role_       |
| `manager`    | **Approve/reject return & refund requests** — explicit manager role; same approval privileges as `instructor`          |
| `clerk`      | Distribution events, ledger, backorder management, inbox                                                               |
| `moderator`  | Review and act on reported comments, inbox                                                                             |
| `admin`      | Full access: user management, analytics, all settings, all of the above                                                |

> **Manager role:** The prompt specification calls for a "manager" role to approve return and
> refund requests.  This system supports **both** `manager` (explicit) and `instructor`
> (historical alias) for that workflow.  Routes `GET /admin/returns`,
> `POST /admin/returns/:id/approve`, and `POST /admin/returns/:id/reject` accept
> `instructor`, `manager`, and `admin`.  The service-layer check in
> `internal/services/orders.go` (`ApproveReturn`) enforces the same three roles.

## Project Structure

```
w2t86/
├── cmd/
│   └── server/
│       └── main.go              # Entry point: wires repos, services, handlers, routes
├── internal/
│   ├── config/                  # Environment-based configuration
│   ├── crypto/                  # Password hashing + AES-256-GCM helpers
│   ├── db/                      # SQLite open + migration runner
│   ├── handlers/                # HTTP handlers (one file per domain)
│   │   ├── admin.go             # User management, custom fields, audit log
│   │   ├── analytics.go         # Dashboard stats, exports, geospatial map
│   │   ├── auth.go              # Login / logout
│   │   ├── courses.go           # Course plans (instructor)
│   │   ├── distribution.go      # Issue, return, exchange, reissue, ledger
│   │   ├── materials.go         # Browse, detail, rating, comments, favorites, share
│   │   ├── messages.go          # Inbox, SSE, DND, subscriptions
│   │   └── orders.go            # Place, pay, cancel, returns, admin views
│   ├── middleware/
│   │   ├── auth.go              # Session validation, GetUser helper
│   │   ├── ratelimit.go         # Sliding-window rate limiter (comments)
│   │   └── rbac.go              # Role-based access control (RequireRole)
│   ├── models/
│   │   └── models.go            # Go structs for every DB table
│   ├── observability/           # Structured loggers, request logger, metrics
│   ├── repository/              # Data access layer (one file per domain)
│   ├── scheduler/
│   │   └── scheduler.go         # Cron: auto-close stale orders every minute
│   ├── services/                # Business logic (one file per domain)
│   └── testutil/                # Shared in-memory DB helper for tests
├── migrations/                  # Numbered SQL migration files (001–016)
├── API_tests/                   # Black-box HTTP API tests (Fiber test runner)
├── unit_tests/                  # Pure unit tests (state machine, validation, etc.)
├── web/
│   ├── static/
│   │   ├── css/                 # Bootstrap, Bootstrap Icons, Leaflet, app.css
│   │   └── js/                  # HTMX, Alpine.js, Bootstrap bundle, Leaflet, app.js
│   └── templates/               # Go html/template files
│       ├── layouts/             # base.html (sidebar), main.html (login shell)
│       ├── admin/               # Admin panel pages
│       ├── analytics/           # Dashboard and geospatial map pages
│       ├── courses/             # Course plan pages
│       ├── distribution/        # Clerk distribution pages
│       ├── history/             # Browse history page
│       ├── inbox/               # Inbox, settings
│       ├── materials/           # Material list, detail
│       ├── moderation/          # Moderation queue
│       ├── orders/              # Order list, cart, detail
│       └── partials/            # Reusable HTMX partial fragments
├── .env                         # Local secrets — gitignored, never committed
├── .env.example                 # Placeholder template — safe to commit
├── go.mod
├── go.sum
└── README.md
```
