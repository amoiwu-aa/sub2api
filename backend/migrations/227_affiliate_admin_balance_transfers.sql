-- 分销管理员余额划转流水（含幂等键与划转前后余额快照）。
-- Affiliate-admin balance transfers with idempotency and before/after snapshots.

CREATE TABLE IF NOT EXISTS affiliate_admin_balance_transfers (
    id                    BIGSERIAL PRIMARY KEY,
    affiliate_admin_id    BIGINT NOT NULL REFERENCES users (id),
    target_user_id        BIGINT NOT NULL REFERENCES users (id),
    amount                DECIMAL(20, 8) NOT NULL,
    source_balance_before DECIMAL(20, 8) NOT NULL,
    source_balance_after  DECIMAL(20, 8) NOT NULL,
    target_balance_before DECIMAL(20, 8) NOT NULL,
    target_balance_after  DECIMAL(20, 8) NOT NULL,
    idempotency_key       VARCHAR(64) NOT NULL,
    notes                 TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT affiliate_admin_balance_transfers_amount_positive CHECK (amount > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_affiliate_admin_balance_transfers_admin_idempotency
    ON affiliate_admin_balance_transfers (affiliate_admin_id, idempotency_key);

CREATE INDEX IF NOT EXISTS idx_affiliate_admin_balance_transfers_admin_created_at
    ON affiliate_admin_balance_transfers (affiliate_admin_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_affiliate_admin_balance_transfers_target_created_at
    ON affiliate_admin_balance_transfers (target_user_id, created_at DESC);

COMMENT ON TABLE affiliate_admin_balance_transfers IS '分销管理员向名下用户划转余额的流水 / Affiliate-admin to managed-user balance transfers';
COMMENT ON COLUMN affiliate_admin_balance_transfers.affiliate_admin_id IS '划出方分销管理员 / Source affiliate admin';
COMMENT ON COLUMN affiliate_admin_balance_transfers.target_user_id IS '划入方用户 / Target user';
COMMENT ON COLUMN affiliate_admin_balance_transfers.amount IS '划转金额，必须大于 0 / Transfer amount, must be > 0';
COMMENT ON COLUMN affiliate_admin_balance_transfers.idempotency_key IS '同一分销管理员下的幂等键 / Per-admin idempotency key';
