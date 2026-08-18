-- 后台开号的操作者，用于分销管理员只看自己创建的用户。
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS created_by_admin_id BIGINT NULL REFERENCES users (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_users_created_by_admin_id
    ON users (created_by_admin_id)
    WHERE created_by_admin_id IS NOT NULL AND deleted_at IS NULL;
