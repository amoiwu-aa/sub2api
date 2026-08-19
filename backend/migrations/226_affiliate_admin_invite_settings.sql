-- 分销管理员开号邀请默认开关与默认分组。
-- Invite defaults for affiliate admins (enabled flag + default groups).

CREATE TABLE IF NOT EXISTS affiliate_admin_invite_settings (
    affiliate_admin_id BIGINT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE affiliate_admin_invite_settings IS '分销管理员开号邀请默认开关 / Affiliate-admin invite defaults';
COMMENT ON COLUMN affiliate_admin_invite_settings.affiliate_admin_id IS '分销管理员用户 ID / Affiliate admin user id';
COMMENT ON COLUMN affiliate_admin_invite_settings.enabled IS '是否允许该分销管理员使用邀请开号 / Whether invite-on-create is enabled';

CREATE TABLE IF NOT EXISTS affiliate_admin_invite_groups (
    affiliate_admin_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    group_id           BIGINT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (affiliate_admin_id, group_id)
);

-- group_id 不加 groups FK：分组删除时由应用层调用 RemoveDefaultGroupID 清理。
-- No groups FK: group deletion is cleaned up later via RemoveDefaultGroupID.
CREATE INDEX IF NOT EXISTS idx_affiliate_admin_invite_groups_group_id
    ON affiliate_admin_invite_groups (group_id);

COMMENT ON TABLE affiliate_admin_invite_groups IS '分销管理员开号默认分组 / Default groups applied when an affiliate admin creates users';
COMMENT ON COLUMN affiliate_admin_invite_groups.affiliate_admin_id IS '分销管理员用户 ID / Affiliate admin user id';
COMMENT ON COLUMN affiliate_admin_invite_groups.group_id IS '默认分组 ID / Default group id';
