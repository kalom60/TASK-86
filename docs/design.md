# District Materials Commerce & Logistics Portal — Design Document

## 1. System Overview

The portal is a role-aware web application for district-wide educational material operations.

Primary roles:
- Admin
- Student
- Instructor
- Manager
- Clerk
- Moderator

Core capabilities:
- authentication and session management
- material catalog search (including FTS)
- ordering, payment confirmation, and cancellation
- return / exchange / refund workflows
- distribution and custody tracking
- instructor course planning and approvals
- moderation queue for reported comments
- inbox notifications with DND + subscriptions
- admin analytics, audit, duplicate detection, and custom fields

Technology stack:
- Go + Fiber + html/template
- SQLite (WAL mode) with migrations
- HTMX + Alpine.js + Bootstrap + Leaflet

---

## 2. Design Goals

- deterministic service-layer business logic
- strict separation: handlers -> services -> repositories
- secure handling of sessions and sensitive fields
- offline-capable frontend assets (no CDN dependency)
- maintainable, testable modules with layered boundaries
- full auditability for critical administrative actions

---

## 3. High-Level Architecture

The application follows a server-rendered layered architecture:

Web UI (templates + HTMX partials + Alpine)
↓
HTTP Handlers (request parsing / rendering)
↓
Services (business rules + orchestration)
↓
Repositories (SQL access)
↓
SQLite (WAL, FK enforcement, migrations)

Cross-cutting modules:
- middleware (auth, RBAC, rate limiting)
- observability (structured logging, request IDs, in-memory metrics)
- scheduler (auto-close stale orders)
- crypto utilities (password hashing, AES-GCM field encryption, blind indexes)

### Architecture Principle

Business rules are enforced in services, while repositories remain persistence-focused.

---

## 4. Runtime Composition

`cmd/server/main.go` wires the system in startup order:
- configuration load + validation
- DB open + migration run
- admin bootstrap credential rotation (if placeholder/legacy hash detected)
- repository initialization
- service initialization
- handler initialization
- middleware, routes, scheduler, and graceful shutdown

---

## 5. Frontend Architecture

### 5.1 Rendering Model

- server-rendered HTML templates (`web/templates`)
- HTMX for partial page updates
- Alpine.js for lightweight UI behavior
- Bootstrap for layout/components

### 5.2 Major Route Areas

- auth: `/login`, `/logout`, `/account/change-password`
- student/common: `/dashboard`, `/materials`, `/orders`, `/favorites`, `/history`, `/inbox`
- clerk/admin: `/distribution`, `/admin/orders`
- moderation: `/moderation`
- instructor/manager/admin: `/courses`, `/admin/returns`, `/dashboard/instructor`
- admin-only: `/dashboard/admin`, `/admin/users`, `/admin/materials`, `/admin/audit`, `/analytics/*`

### 5.3 Realtime UX

- SSE inbox stream at `/inbox/sse`
- unread badge endpoints for live notification counts

---

## 6. Security & Access Control Design

Authentication/session:
- password hashing via bcrypt helpers
- account lockout after 5 failed attempts for 15 minutes
- session token = random 32-byte hex; DB stores HMAC-SHA256 token hash
- mandatory password reset when `must_change_password = 1`

Authorization:
- route-level RBAC middleware
- manager workflow accepts `instructor`, `manager`, and `admin`

Request protection:
- CSRF on authenticated state-mutating routes
- comment rate limiting middleware
- login rate limiting per IP

Data security:
- AES-256-GCM for sensitive user/custom fields
- HMAC blind indexes for duplicate detection (`full_name_idx`, `external_id_idx`)
- optional phonetic index (`full_name_phonetic`) for privacy-preserving fuzzy matching

---

## 7. Service Layer Design

### 7.1 AuthService

Responsibilities:
- login/logout/session lifecycle
- lockout policy
- password change and registration

### 7.2 MaterialService

Responsibilities:
- catalog CRUD orchestration
- FTS-backed search
- comments, ratings, favorites, share links
- browse history

Rules:
- comment length <= 500
- max 2 links per comment
- word filter support from `BANNED_WORDS`
- comment throttling window

### 7.3 OrderService

Responsibilities:
- place orders with inventory validation
- payment confirmation
- status transitions and cancellation policy
- return/exchange/refund request lifecycle

Rules include:
- students cancel only own `pending_payment` orders
- admins/instructors broader cancellation rights
- 14-day return window from `completed_at`
- one pending return request per order

### 7.4 DistributionService

Responsibilities:
- item issue, return, exchange, reissue
- custody event recording with `scan_id`
- backorder handling on partial fulfillment

### 7.5 MessagingService

Responsibilities:
- notification creation and inbox retrieval
- unread counters and mark-read flows
- DND settings
- topic subscription controls

### 7.6 ModerationService

Responsibilities:
- moderation queue retrieval
- approve/remove collapsed comments

### 7.7 CourseService

Responsibilities:
- course + section management
- per-section material planning
- plan approval workflow

### 7.8 AnalyticsService

Responsibilities:
- admin and instructor dashboard KPIs
- geospatial computations and queries
- export support

### 7.9 AdminService

Responsibilities:
- user administration and role changes
- encrypted custom fields
- duplicate detection and merges
- audit log retrieval

---

## 8. Persistence Design

### 8.1 Database Engine

- SQLite with WAL mode
- foreign keys enabled
- busy timeout configured
- migrations applied in lexicographic order with `_migrations` tracking

### 8.2 Core Table Groups

Users/auth:
- `users`, `sessions`, `entity_custom_fields`, `entity_custom_fields_audit`

Catalog/engagement:
- `materials`, `materials_fts`, `material_versions`, `ratings`, `comments`, `comment_reports`, `favorites_*`, `browse_history`

Commerce/logistics:
- `orders`, `order_items`, `order_events`, `backorders`, `return_requests`, `distribution_events`, `financial_transactions`

Messaging:
- `notifications`, `subscriptions`, `dnd_settings`

Analytics/admin:
- `locations`, `location_rtree`, `spatial_aggregates`, `kpi_snapshots`, `audit_log`, `entity_duplicates`

Course planning:
- `courses`, `course_sections`, `course_plans`

### 8.3 Persistence Principle

Repositories are the only layer that executes SQL.

---

## 9. Domain Models (Key)

### 9.1 User

- identity and role
- auth hardening fields (`failed_attempts`, `locked_until`, `must_change_password`)
- encrypted PII + indexes (`full_name`, `external_id`, blind indexes, phonetic)

### 9.2 Material

- bibliographic fields and inventory counters
- `price` as authoritative server-side order price

### 9.3 Order

- lifecycle status + totals
- `auto_close_at` for scheduler
- `completed_at` for return eligibility window

### 9.4 ReturnRequest

- type (`return`, `exchange`, `refund`)
- status and resolution metadata
- optional `replacement_material_id`

### 9.5 DistributionEvent

- physical handling events (`issued`, `returned`, etc.)
- custody chain and scan traceability

### 9.6 Notification

- per-user inbox entries
- read/delivered timestamps
- optional entity references

### 9.7 Course / Section / Plan

- instructor-owned course structure
- section-scoped requested and approved quantities

---

## 10. Order Lifecycle Workflow

Nominal flow:
- `pending_payment` -> `pending_shipment` -> `in_transit` -> `completed`

Alternative terminal flow:
- `canceled`

Key rules:
- transitions are validated in service/repository logic
- inventory reservation/release is coupled to transitions
- financial records are written for payment/refund traceability

---

## 11. Returns, Exchanges, and Refunds Workflow

Submission:
- only order owner
- order must be `completed`
- within 14 days of `completed_at`

Approval:
- manager-capable roles (`admin`, `instructor`, `manager`)
- exchange requires replacement stock check
- refund/return approvals create financial transaction records

Execution:
- distribution return events adjust stock and custody chain

---

## 12. Distribution & Custody Design

- clerk/admin operations record immutable distribution events
- partial issuance creates explicit backorders
- custody transitions tracked with `custody_from`/`custody_to`
- scan-based lookup supports chain-of-custody review

---

## 13. Messaging & Notification Design

- topic-based subscriptions gate notification delivery
- DND windows defer `delivered_at`
- inbox supports pagination, single/bulk read updates
- SSE endpoint enables near-realtime inbox updates

---

## 14. Moderation Design

- users can report comments
- reported comments enter moderation queue (collapsed state)
- moderator/admin may approve (restore) or remove
- moderation actions are logged through service + repository flows

---

## 15. Analytics & Geospatial Design

Admin analytics:
- order status mix, fulfillment, return rate
- active users, inventory levels, top materials
- funnel, GMV, AOV, conversion, repeat purchase

Instructor analytics:
- course order statistics
- pending course-plan approvals

Geospatial:
- location layers and aggregate computations
- radius/buffer and POI density queries
- region aggregates and trajectory chains
- R*Tree index for spatial prefiltering

---

## 16. Scheduler Design

`OrderScheduler` runs every minute:
- auto-closes expired `pending_payment` orders
- auto-closes expired `pending_shipment` orders

Each close operation is transactional and includes:
- status update to `canceled`
- inventory rollback
- `order_events` audit entry

---

## 17. Observability Design

Logging:
- structured logger with domain channels (`auth`, `orders`, `security`, etc.)
- request ID and request logging middleware

Metrics:
- in-memory atomic counters for requests, auth outcomes, order events
- admin-gated `/metrics` endpoint
- `/health` endpoint with DB ping + uptime

---

## 18. Error Handling Strategy

- services return explicit validation and authorization errors
- central Fiber error handler for JSON/HTMX-safe responses
- no silent failures for critical state transitions
- best-effort handling only for non-critical side effects (for example version-write hints)

---

## 19. Testing Strategy

Layers covered by tests:
- unit tests (`unit_tests/`)
- service/repository tests (`internal/services`, `internal/repository`)
- API black-box tests (`API_tests/`)
- integration tests (`internal/integration/`)

Testing properties:
- SQLite in-memory test DB
- deterministic lifecycle and permission assertions
- security-focused tests (auth, rate limit, credential integrity)

---

## 20. Implementation Constraints

- backend is monolithic Go service with SQLite
- all stateful rules must remain in services/repositories
- schema changes must be migration-driven
- secrets required at startup (`ENCRYPTION_KEY`, `SESSION_SECRET`)
- static assets are vendored locally to support offline/no-CDN runtime

---

## 21. Future-Ready Extension Points

- replace repository implementations while preserving service contracts
- promote in-memory metrics to external metrics backend
- add async workers beyond cron scheduler for heavier jobs
- introduce external identity provider while preserving RBAC policy model
