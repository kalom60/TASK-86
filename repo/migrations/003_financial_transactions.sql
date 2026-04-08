-- 003_financial_transactions.sql
-- Adds the financial_transactions table for return/refund/charge traceability.

CREATE TABLE IF NOT EXISTS financial_transactions (
    id                INTEGER PRIMARY KEY,
    order_id          INTEGER REFERENCES orders(id),
    return_request_id INTEGER REFERENCES return_requests(id),
    type              TEXT    NOT NULL,  -- 'refund' | 'charge' | 'adjustment'
    amount            REAL    NOT NULL DEFAULT 0,
    status            TEXT    DEFAULT 'pending',  -- 'pending' | 'completed' | 'failed'
    reference         TEXT,
    note              TEXT,
    actor_id          INTEGER REFERENCES users(id),
    created_at        TEXT    DEFAULT (datetime('now')),
    updated_at        TEXT    DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_financial_txn_order  ON financial_transactions(order_id);
CREATE INDEX IF NOT EXISTS idx_financial_txn_return ON financial_transactions(return_request_id);
