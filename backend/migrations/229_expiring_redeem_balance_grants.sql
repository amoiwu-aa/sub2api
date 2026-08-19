-- Balance redeem codes may grant credit that must be consumed within a fixed
-- number of days after redemption. The wallet balance remains the aggregate
-- amount; this ledger tracks the unspent expiring portion.

ALTER TABLE redeem_codes
ADD COLUMN IF NOT EXISTS balance_validity_days INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS user_balance_grants (
    id               BIGSERIAL PRIMARY KEY,
    user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redeem_code_id   BIGINT REFERENCES redeem_codes(id) ON DELETE SET NULL,
    original_amount  DECIMAL(20, 8) NOT NULL,
    remaining_amount DECIMAL(20, 8) NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    exhausted_at     TIMESTAMPTZ,
    expired_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_balance_grants_amount_positive CHECK (original_amount > 0),
    CONSTRAINT user_balance_grants_remaining_nonnegative CHECK (remaining_amount >= 0),
    CONSTRAINT user_balance_grants_remaining_bounded CHECK (remaining_amount <= original_amount)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_balance_grants_redeem_code
    ON user_balance_grants(redeem_code_id)
    WHERE redeem_code_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_user_balance_grants_active_expiry
    ON user_balance_grants(user_id, expires_at, id)
    WHERE remaining_amount > 0 AND expired_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_balance_grants_expiry
    ON user_balance_grants(expires_at)
    WHERE remaining_amount > 0 AND expired_at IS NULL;

COMMENT ON COLUMN redeem_codes.balance_validity_days IS
    'Days the credited balance remains usable after redemption; 0 means permanent';
COMMENT ON TABLE user_balance_grants IS
    'Unspent portions of time-limited balance grants, consumed by earliest expiry first';
