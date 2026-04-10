# Verification Report for `audit_report_1_fix-check_round2.md`

Date: 2026-04-10
Reviewer: Codex
Scope: Static verification of the six listed issues against the current codebase, plus a focused test run.

## Summary
- Total issues reviewed: 6
- Fixed: 6
- Partially fixed: 0
- Not fixed: 0

## Issue-by-Issue Status

### 1) High — Comment anti-spam timestamp mismatch
Status: **Fixed**

Evidence:
- `CountRecentComments` uses UNIX epoch comparison to avoid datetime string-format mismatch: `repo/internal/repository/engagement.go:249-257`.
- Test validates both recent-window and future-cutoff behavior: `repo/internal/repository/engagement_test.go:249-279`.
- Focused test run passed from `repo/`:
  - `go test -tags sqlite_fts5 ./internal/repository -run 'TestEngagementRepository_CountRecentComments_RateLimit|TestEngagementRepository_GenerateShareToken'`

---

### 2) High — Geospatial compute CSRF mismatch
Status: **Fixed**

Evidence:
- CSRF middleware requires header/form token: `repo/cmd/server/main.go:280-294`.
- `/analytics/map/compute` remains CSRF-protected: `repo/cmd/server/main.go:446`.
- Frontend compute request sends `X-Csrf-Token`: `repo/web/static/js/map.js:208-213`.

---

### 3) High — Startup/test docs inconsistency (.env and sqlite_fts5 prerequisites)
Status: **Fixed**

Evidence:
- Startup loads `.env`: `repo/cmd/server/main.go:35`.
- README run command includes sqlite FTS5 tag: `repo/README.md:64`.
- `run_tests.sh` includes sqlite FTS5 tag in `go test`: `repo/run_tests.sh:109`.
- Makefile remains consistent on build/run/test/lint tags: `repo/Makefile:4,7,25,28,31`.

---

### 4) High — Favorites visibility not enforced
Status: **Fixed**

Evidence:
- Private lists are denied via share token (`sql.ErrNoRows`): `repo/internal/services/materials.go:381-392`.
- Shared endpoint handles the denied/expired states correctly: `repo/internal/handlers/materials.go:551-559`.

---

### 5) Medium — Entity-profile extensibility only for users
Status: **Fixed**

Evidence:
- Generic routes exist for entity-scoped fields and remain wired: `repo/cmd/server/main.go:430-432`.
- Handler resolves generic `entity_type`/`entity_id` and renders generic context (`EntityType`, `EntityDisplayName`, `TargetID`, `Fields`): `repo/internal/handlers/admin.go:227-250`.
- Template now renders a generic entity header always, and renders fields/audit column full-width when `.TargetUser` is absent (non-user entities):
  - `repo/web/templates/admin/users/profile.html:8-12`
  - `repo/web/templates/admin/users/profile.html:48-54`

Assessment:
- Backend, routes, and admin UI flow now support non-user entity field management in the shared page.

---

### 6) Medium — Tests caveat vs production-consistent behavior
Status: **Fixed**

Evidence:
- Rate-limit test now asserts production-consistent behavior directly for time windows: `repo/internal/repository/engagement_test.go:249-279`.

---

## Final Verdict
**Pass.** All previously listed issues are fixed.

