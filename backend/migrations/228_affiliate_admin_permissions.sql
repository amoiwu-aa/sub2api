-- Delegated permissions for affiliate/distribution administrators.
-- Permissions are deny-by-default and only effective while the target user
-- remains an active affiliate_admin.

CREATE TABLE IF NOT EXISTS affiliate_admin_permissions (
    affiliate_admin_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permission_key VARCHAR(64) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (affiliate_admin_id, permission_key)
);

CREATE INDEX IF NOT EXISTS idx_affiliate_admin_permissions_enabled
    ON affiliate_admin_permissions(permission_key, enabled)
    WHERE enabled = TRUE;

COMMENT ON TABLE affiliate_admin_permissions IS
    'Per-affiliate-admin delegated permissions; deny by default';
COMMENT ON COLUMN affiliate_admin_permissions.permission_key IS
    'Stable permission key, for example announcement.publish';

-- A demoted distribution administrator must not regain stale permissions if
-- the account is promoted again later.
CREATE OR REPLACE FUNCTION clear_affiliate_admin_permissions_on_role_change()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.role = 'affiliate_admin' AND NEW.role IS DISTINCT FROM OLD.role THEN
        DELETE FROM affiliate_admin_permissions
        WHERE affiliate_admin_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_clear_affiliate_admin_permissions_on_role_change ON users;
CREATE TRIGGER trg_clear_affiliate_admin_permissions_on_role_change
AFTER UPDATE OF role ON users
FOR EACH ROW
EXECUTE FUNCTION clear_affiliate_admin_permissions_on_role_change();
