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

3. **Download HTMX and Alpine.js**

   Place the minified files in `web/static/js/`:

   ```bash
   # HTMX 2.x
   curl -Lo web/static/js/htmx.min.js \
     https://unpkg.com/htmx.org@2/dist/htmx.min.js

   # Alpine.js 3.x
   curl -Lo web/static/js/alpine.min.js \
     https://unpkg.com/alpinejs@3/dist/cdn.min.js
   ```

4. **Configure environment**

   Create a `.env` file in the project root (it is gitignored — never commit it):

   ```bash
   # Generate the two required secrets
   echo "ENCRYPTION_KEY=$(openssl rand -hex 32)" >> .env
   echo "SESSION_SECRET=$(openssl rand -hex 32)" >> .env

   # Add the remaining variables with your preferred values
   echo "PORT=3000"            >> .env
   echo "DB_PATH=data/portal.db" >> .env
   echo "APP_ENV=development"  >> .env
   echo "BANNED_WORDS="        >> .env
   ```

   See the [Environment Variables](#environment-variables) section below for a
   description of every variable.

5. **Run the server**

   ```bash
   go run ./cmd/server
   ```

   The server listens on `http://localhost:3000` by default.

## Docker

### Quick start
```bash
# Create .env with generated secrets (gitignored — never commit this file)
echo "ENCRYPTION_KEY=$(openssl rand -hex 32)" >  .env
echo "SESSION_SECRET=$(openssl rand -hex 32)" >> .env
echo "PORT=3000"             >> .env
echo "DB_PATH=/app/data/portal.db" >> .env
echo "APP_ENV=production"    >> .env

docker compose up -d
```

### Development (with live template reload)
```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up
```

### Useful commands
```bash
make docker-logs     # tail logs
make docker-down     # stop
make test            # run tests locally
```

## Environment Variables

The application reads all configuration from environment variables.
There is **no committed `.env` file** — create one locally (it is gitignored) or
inject variables through your deployment platform.

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

## Default Credentials

| Username | Password       | Role  |
|----------|----------------|-------|
| `admin`  | `ChangeMe123!` | admin |

**Change the admin password immediately after first login.**

The default admin account is inserted by the initial migration. Update it via the Admin Settings page or directly in the database.

## Available Roles

| Role         | Capabilities                                                        |
|--------------|---------------------------------------------------------------------|
| `student`    | Browse materials, place orders, manage favorites, inbox             |
| `instructor` | Course plans, approve orders, inbox                                 |
| `clerk`      | Distribution events, ledger, backorder management, inbox            |
| `moderator`  | Review and act on reported comments, inbox                          |
| `admin`      | Full access: user management, analytics, all settings, all of above |

## Project Structure

```
w2t86/
├── cmd/
│   └── server/
│       └── main.go              # Entry point: wires and starts the server
├── internal/
│   ├── config/
│   │   └── config.go            # Environment-based configuration
│   ├── crypto/
│   │   └── crypto.go            # Password hashing + AES-256-GCM helpers
│   ├── db/
│   │   └── db.go                # SQLite open + migration runner
│   ├── handlers/
│   │   └── auth.go              # Login / logout HTTP handlers
│   ├── middleware/
│   │   ├── auth.go              # Session validation, GetUser helper
│   │   ├── ratelimit.go         # Sliding-window rate limiter
│   │   └── rbac.go              # Role-based access control
│   ├── models/
│   │   └── models.go            # Go structs for every DB table
│   ├── repository/
│   │   ├── sessions.go          # Session CRUD
│   │   └── users.go             # User CRUD
│   ├── scheduler/
│   │   └── scheduler.go         # Cron jobs: auto-close stale orders
│   └── services/
│       └── auth.go              # Login, logout, register business logic
├── migrations/
│   └── 001_schema.sql           # Full database schema
├── web/
│   ├── static/
│   │   ├── css/
│   │   │   └── app.css          # Hand-crafted application styles
│   │   └── js/
│   │       ├── app.js           # Vanilla JS utilities (toast, confirm, etc.)
│   │       ├── alpine.min.js    # Alpine.js 3.x (download separately)
│   │       └── htmx.min.js      # HTMX 2.x (download separately)
│   └── templates/
│       ├── layouts/
│       │   ├── base.html        # Full layout: sidebar + topbar (authenticated)
│       │   └── main.html        # Minimal layout: HTML shell (login page)
│       ├── pages/               # (reserved for future page templates)
│       ├── partials/
│       │   ├── login_form.html  # Login form partial (HTMX swap target)
│       │   └── toast.html       # Toast notification partial (hx-swap-oob)
│       ├── login.html           # Login page content
│       └── dashboard.html       # Dashboard page content
├── .env                         # Local secrets — gitignored, never committed
├── go.mod
├── go.sum
└── README.md
```
