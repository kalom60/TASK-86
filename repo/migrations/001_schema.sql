-- ============================================================
-- 001_schema.sql  –  full portal schema (SQLite / WAL)
-- ============================================================

-- -------------------------------------------------------
-- Users & Auth
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS users (
    id              INTEGER PRIMARY KEY,
    username        TEXT    UNIQUE NOT NULL,
    email           TEXT    UNIQUE NOT NULL,
    password_hash   TEXT    NOT NULL,
    role            TEXT    NOT NULL DEFAULT 'student',
    failed_attempts INTEGER DEFAULT 0,
    locked_until    TEXT,
    created_at      TEXT    DEFAULT (datetime('now')),
    updated_at      TEXT    DEFAULT (datetime('now')),
    deleted_at      TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT    UNIQUE NOT NULL,
    expires_at  TEXT    NOT NULL,
    created_at  TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS user_custom_fields (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    field_name   TEXT    NOT NULL,
    field_value  TEXT,
    is_encrypted INTEGER DEFAULT 0,
    UNIQUE(user_id, field_name)
);

-- -------------------------------------------------------
-- Materials
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS materials (
    id            INTEGER PRIMARY KEY,
    isbn          TEXT,
    title         TEXT    NOT NULL,
    author        TEXT,
    publisher     TEXT,
    edition       TEXT,
    subject       TEXT,
    grade_level   TEXT,
    total_qty     INTEGER DEFAULT 0,
    available_qty INTEGER DEFAULT 0,
    reserved_qty  INTEGER DEFAULT 0,
    status        TEXT    DEFAULT 'active',
    created_at    TEXT    DEFAULT (datetime('now')),
    updated_at    TEXT    DEFAULT (datetime('now')),
    deleted_at    TEXT
);

-- Full-text search virtual table for materials.
CREATE VIRTUAL TABLE IF NOT EXISTS materials_fts
USING fts5(title, author, subject, content='materials', content_rowid='id');

-- Keep FTS index in sync with the base table.
CREATE TRIGGER IF NOT EXISTS materials_fts_ai
AFTER INSERT ON materials BEGIN
    INSERT INTO materials_fts(rowid, title, author, subject)
    VALUES (new.id, new.title, new.author, new.subject);
END;

CREATE TRIGGER IF NOT EXISTS materials_fts_ad
AFTER DELETE ON materials BEGIN
    INSERT INTO materials_fts(materials_fts, rowid, title, author, subject)
    VALUES ('delete', old.id, old.title, old.author, old.subject);
END;

CREATE TRIGGER IF NOT EXISTS materials_fts_au
AFTER UPDATE ON materials BEGIN
    INSERT INTO materials_fts(materials_fts, rowid, title, author, subject)
    VALUES ('delete', old.id, old.title, old.author, old.subject);
    INSERT INTO materials_fts(rowid, title, author, subject)
    VALUES (new.id, new.title, new.author, new.subject);
END;

CREATE TABLE IF NOT EXISTS material_versions (
    id          INTEGER PRIMARY KEY,
    material_id INTEGER NOT NULL REFERENCES materials(id),
    changed_by  INTEGER REFERENCES users(id),
    change_data TEXT    NOT NULL,
    changed_at  TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS ratings (
    id          INTEGER PRIMARY KEY,
    material_id INTEGER NOT NULL REFERENCES materials(id),
    user_id     INTEGER NOT NULL REFERENCES users(id),
    stars       INTEGER NOT NULL CHECK(stars BETWEEN 1 AND 5),
    created_at  TEXT    DEFAULT (datetime('now')),
    UNIQUE(material_id, user_id)
);

CREATE TABLE IF NOT EXISTS comments (
    id           INTEGER PRIMARY KEY,
    material_id  INTEGER NOT NULL REFERENCES materials(id),
    user_id      INTEGER NOT NULL REFERENCES users(id),
    body         TEXT    NOT NULL,
    link_count   INTEGER DEFAULT 0,
    status       TEXT    DEFAULT 'active',
    report_count INTEGER DEFAULT 0,
    created_at   TEXT    DEFAULT (datetime('now')),
    updated_at   TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS comment_reports (
    id          INTEGER PRIMARY KEY,
    comment_id  INTEGER NOT NULL REFERENCES comments(id),
    reported_by INTEGER NOT NULL REFERENCES users(id),
    reason      TEXT,
    created_at  TEXT    DEFAULT (datetime('now')),
    UNIQUE(comment_id, reported_by)
);

CREATE TABLE IF NOT EXISTS favorites_lists (
    id              INTEGER PRIMARY KEY,
    user_id         INTEGER NOT NULL REFERENCES users(id),
    name            TEXT    NOT NULL,
    visibility      TEXT    DEFAULT 'private',
    share_token     TEXT    UNIQUE,
    share_expires_at TEXT,
    created_at      TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS favorites_items (
    id          INTEGER PRIMARY KEY,
    list_id     INTEGER NOT NULL REFERENCES favorites_lists(id) ON DELETE CASCADE,
    material_id INTEGER NOT NULL REFERENCES materials(id),
    added_at    TEXT    DEFAULT (datetime('now')),
    UNIQUE(list_id, material_id)
);

CREATE TABLE IF NOT EXISTS browse_history (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    material_id INTEGER NOT NULL REFERENCES materials(id),
    visited_at  TEXT    DEFAULT (datetime('now'))
);

-- -------------------------------------------------------
-- Orders
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS orders (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users(id),
    status        TEXT    NOT NULL DEFAULT 'pending_payment',
    total_amount  REAL    DEFAULT 0,
    auto_close_at TEXT,
    created_at    TEXT    DEFAULT (datetime('now')),
    updated_at    TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS order_items (
    id                 INTEGER PRIMARY KEY,
    order_id           INTEGER NOT NULL REFERENCES orders(id),
    material_id        INTEGER NOT NULL REFERENCES materials(id),
    qty                INTEGER NOT NULL,
    unit_price         REAL    DEFAULT 0,
    fulfillment_status TEXT    DEFAULT 'pending'
);

CREATE TABLE IF NOT EXISTS order_events (
    id          INTEGER PRIMARY KEY,
    order_id    INTEGER NOT NULL REFERENCES orders(id),
    from_status TEXT,
    to_status   TEXT    NOT NULL,
    actor_id    INTEGER REFERENCES users(id),
    note        TEXT,
    created_at  TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS backorders (
    id            INTEGER PRIMARY KEY,
    order_item_id INTEGER NOT NULL REFERENCES order_items(id),
    qty           INTEGER NOT NULL,
    resolved_at   TEXT,
    resolved_by   INTEGER REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS return_requests (
    id           INTEGER PRIMARY KEY,
    order_id     INTEGER NOT NULL REFERENCES orders(id),
    user_id      INTEGER NOT NULL REFERENCES users(id),
    type         TEXT    NOT NULL,
    status       TEXT    DEFAULT 'pending',
    reason       TEXT,
    requested_at TEXT    DEFAULT (datetime('now')),
    resolved_at  TEXT,
    resolved_by  INTEGER REFERENCES users(id)
);

-- -------------------------------------------------------
-- Distribution
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS distribution_events (
    id           INTEGER PRIMARY KEY,
    order_id     INTEGER REFERENCES orders(id),
    material_id  INTEGER NOT NULL REFERENCES materials(id),
    qty          INTEGER NOT NULL,
    event_type   TEXT    NOT NULL,
    scan_id      TEXT,
    actor_id     INTEGER REFERENCES users(id),
    custody_from TEXT,
    custody_to   TEXT,
    occurred_at  TEXT    DEFAULT (datetime('now'))
);

-- -------------------------------------------------------
-- Messaging
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS notifications (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id),
    type         TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    body         TEXT,
    ref_id       INTEGER,
    ref_type     TEXT,
    read_at      TEXT,
    delivered_at TEXT,
    created_at   TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id),
    topic      TEXT    NOT NULL,
    active     INTEGER DEFAULT 1,
    created_at TEXT    DEFAULT (datetime('now')),
    UNIQUE(user_id, topic)
);

CREATE TABLE IF NOT EXISTS dnd_settings (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) UNIQUE,
    start_hour INTEGER DEFAULT 21,
    end_hour   INTEGER DEFAULT 7,
    updated_at TEXT    DEFAULT (datetime('now'))
);

-- -------------------------------------------------------
-- Spatial
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS locations (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    type       TEXT,
    geom_wkt   TEXT,
    lat        REAL,
    lng        REAL,
    properties TEXT,
    created_at TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS spatial_aggregates (
    id          INTEGER PRIMARY KEY,
    layer_type  TEXT    NOT NULL,
    cell_key    TEXT    NOT NULL,
    metric      TEXT    NOT NULL,
    value       REAL,
    computed_at TEXT    DEFAULT (datetime('now')),
    UNIQUE(layer_type, cell_key, metric)
);

-- -------------------------------------------------------
-- KPIs / Audit
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS kpi_snapshots (
    id          INTEGER PRIMARY KEY,
    metric_name TEXT    NOT NULL,
    dimension   TEXT,
    value       REAL,
    period      TEXT,
    computed_at TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS audit_log (
    id          INTEGER PRIMARY KEY,
    actor_id    INTEGER REFERENCES users(id),
    action      TEXT    NOT NULL,
    entity_type TEXT    NOT NULL,
    entity_id   INTEGER,
    before_data TEXT,
    after_data  TEXT,
    ip          TEXT,
    created_at  TEXT    DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS entity_duplicates (
    id           INTEGER PRIMARY KEY,
    entity_type  TEXT    NOT NULL,
    primary_id   INTEGER NOT NULL,
    duplicate_id INTEGER NOT NULL,
    status       TEXT    DEFAULT 'pending',
    merged_by    INTEGER REFERENCES users(id),
    merged_at    TEXT
);

-- -------------------------------------------------------
-- Financial
-- -------------------------------------------------------

CREATE TABLE IF NOT EXISTS financial_transactions (
    id                INTEGER PRIMARY KEY,
    order_id          INTEGER REFERENCES orders(id),
    return_request_id INTEGER REFERENCES return_requests(id),
    type              TEXT    NOT NULL,
    amount            REAL    NOT NULL DEFAULT 0,
    status            TEXT    DEFAULT 'pending',
    reference         TEXT,
    note              TEXT,
    actor_id          INTEGER REFERENCES users(id),
    created_at        TEXT    DEFAULT (datetime('now')),
    updated_at        TEXT    DEFAULT (datetime('now'))
);

-- -------------------------------------------------------
-- Indexes
-- -------------------------------------------------------

CREATE INDEX IF NOT EXISTS idx_orders_status      ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_auto_close  ON orders(auto_close_at) WHERE auto_close_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_distribution_scan  ON distribution_events(scan_id);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, read_at);
CREATE INDEX IF NOT EXISTS idx_audit_entity       ON audit_log(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_comments_material  ON comments(material_id, status);
CREATE INDEX IF NOT EXISTS idx_financial_txn_order  ON financial_transactions(order_id);
CREATE INDEX IF NOT EXISTS idx_financial_txn_return ON financial_transactions(return_request_id);

-- -------------------------------------------------------
-- Seed data
-- -------------------------------------------------------

-- Default admin account.  Initial password is 'ChangeMe123!'.
-- IMPORTANT: change this password immediately after first login.
INSERT OR IGNORE INTO users(username, email, password_hash, role)
VALUES('admin', 'admin@portal.local', '$2a$12$fMPISK6tAC1XLVM3JdJQDuB/CrXgdRM.LUPHHu4/VxS/vzihnYyQ.', 'admin');
