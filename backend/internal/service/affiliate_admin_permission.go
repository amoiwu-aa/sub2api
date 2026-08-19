package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AffiliateAdminPermissionPublishAnnouncements = "announcement.publish"

var (
	ErrAffiliateAdminPermissionTarget = infraerrors.BadRequest(
		"AFFILIATE_ADMIN_PERMISSION_TARGET_INVALID",
		"permission target must be an affiliate administrator",
	)
	ErrAffiliateAdminPermissionDenied = infraerrors.Forbidden(
		"AFFILIATE_ADMIN_PERMISSION_DENIED",
		"affiliate administrator permission is required",
	)
)

type AffiliateAdminPermissions struct {
	CanPublishAnnouncements bool `json:"can_publish_announcements"`
}

type AffiliateAdminPermissionRepository interface {
	HasPermission(ctx context.Context, affiliateAdminID int64, permissionKey string) (bool, error)
	SetPermission(ctx context.Context, affiliateAdminID int64, permissionKey string, enabled bool) error
}

type AffiliateAdminPermissionService struct {
	repo     AffiliateAdminPermissionRepository
	userRepo UserRepository
}

func NewAffiliateAdminPermissionService(
	repo AffiliateAdminPermissionRepository,
	userRepo UserRepository,
) *AffiliateAdminPermissionService {
	return &AffiliateAdminPermissionService{repo: repo, userRepo: userRepo}
}

func (s *AffiliateAdminPermissionService) GetPermissions(
	ctx context.Context,
	affiliateAdminID int64,
) (*AffiliateAdminPermissions, error) {
	if err := s.validateAffiliateAdmin(ctx, affiliateAdminID, false); err != nil {
		return nil, err
	}
	allowed, err := s.repo.HasPermission(ctx, affiliateAdminID, AffiliateAdminPermissionPublishAnnouncements)
	if err != nil {
		return nil, err
	}
	return &AffiliateAdminPermissions{CanPublishAnnouncements: allowed}, nil
}

func (s *AffiliateAdminPermissionService) UpdatePermissions(
	ctx context.Context,
	affiliateAdminID int64,
	permissions AffiliateAdminPermissions,
) error {
	if err := s.validateAffiliateAdmin(ctx, affiliateAdminID, false); err != nil {
		return err
	}
	return s.repo.SetPermission(
		ctx,
		affiliateAdminID,
		AffiliateAdminPermissionPublishAnnouncements,
		permissions.CanPublishAnnouncements,
	)
}

func (s *AffiliateAdminPermissionService) CanPublishAnnouncements(
	ctx context.Context,
	affiliateAdminID int64,
) (bool, error) {
	if err := s.validateAffiliateAdmin(ctx, affiliateAdminID, true); err != nil {
		return false, nil
	}
	return s.repo.HasPermission(ctx, affiliateAdminID, AffiliateAdminPermissionPublishAnnouncements)
}

func (s *AffiliateAdminPermissionService) RequirePublishAnnouncements(
	ctx context.Context,
	affiliateAdminID int64,
) error {
	allowed, err := s.CanPublishAnnouncements(ctx, affiliateAdminID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrAffiliateAdminPermissionDenied
	}
	return nil
}

func (s *AffiliateAdminPermissionService) validateAffiliateAdmin(
	ctx context.Context,
	affiliateAdminID int64,
	requireActive bool,
) error {
	if s == nil || s.repo == nil || s.userRepo == nil || affiliateAdminID <= 0 {
		return ErrAffiliateAdminPermissionTarget
	}
	user, err := s.userRepo.GetByID(ctx, affiliateAdminID)
	if err != nil || user == nil || user.Role != RoleAffiliateAdmin {
		return ErrAffiliateAdminPermissionTarget
	}
	if requireActive && user.Status != StatusActive {
		return ErrAffiliateAdminPermissionTarget
	}
	return nil
}
