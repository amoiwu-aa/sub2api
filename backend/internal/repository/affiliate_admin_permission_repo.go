package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type affiliateAdminPermissionRepository struct {
	db *sql.DB
}

var _ service.AffiliateAdminPermissionRepository = (*affiliateAdminPermissionRepository)(nil)

func NewAffiliateAdminPermissionRepository(db *sql.DB) service.AffiliateAdminPermissionRepository {
	return &affiliateAdminPermissionRepository{db: db}
}

func (r *affiliateAdminPermissionRepository) HasPermission(
	ctx context.Context,
	affiliateAdminID int64,
	permissionKey string,
) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("affiliate admin permission repository unavailable")
	}
	var allowed bool
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE((
			SELECT p.enabled
			FROM affiliate_admin_permissions p
			JOIN users u ON u.id = p.affiliate_admin_id
			WHERE p.affiliate_admin_id = $1
			  AND p.permission_key = $2
			  AND u.role = 'affiliate_admin'
			  AND u.deleted_at IS NULL
		), FALSE)
	`, affiliateAdminID, permissionKey).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("query affiliate admin permission: %w", err)
	}
	return allowed, nil
}

func (r *affiliateAdminPermissionRepository) SetPermission(
	ctx context.Context,
	affiliateAdminID int64,
	permissionKey string,
	enabled bool,
) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("affiliate admin permission repository unavailable")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO affiliate_admin_permissions (
			affiliate_admin_id, permission_key, enabled, created_at, updated_at
		)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (affiliate_admin_id, permission_key) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    updated_at = NOW()
	`, affiliateAdminID, permissionKey, enabled)
	if err != nil {
		return fmt.Errorf("set affiliate admin permission: %w", err)
	}
	return nil
}
