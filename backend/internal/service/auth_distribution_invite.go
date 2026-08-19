package service

import (
	"context"
	"errors"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// prepareDistributionInvite resolves an affiliate_admin invite before user create.
// Empty / non-distribution codes return (0, nil, nil) so regular aff codes keep
// fail-open rebate binding. Disabled invite settings fail before create.
func (s *AuthService) prepareDistributionInvite(ctx context.Context, code string) (int64, []int64, error) {
	if s == nil {
		return 0, nil, nil
	}
	if strings.TrimSpace(code) == "" {
		return 0, nil, nil
	}
	if s.affiliateService == nil {
		return 0, nil, nil
	}

	owner, err := s.affiliateService.ResolveDistributionInviter(ctx, code)
	if err != nil {
		if errors.Is(err, ErrDistributionInviteInvalid) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	if owner == nil || owner.ID <= 0 || owner.Role != RoleAffiliateAdmin {
		return 0, nil, nil
	}

	if s.distributionInviteService == nil {
		return owner.ID, nil, nil
	}
	groupIDs, err := s.distributionInviteService.ResolveAndIntersectDefaultGroups(ctx, owner.ID)
	if err != nil {
		return 0, nil, err
	}
	return owner.ID, groupIDs, nil
}

// applyDistributionInvite binds 分销归属 by inviter ID (works with rebate off)
// and applies intersected default groups.
func (s *AuthService) applyDistributionInvite(ctx context.Context, userID, inviterID int64, groupIDs []int64) error {
	if s == nil || userID <= 0 || inviterID <= 0 {
		return nil
	}
	if s.affiliateService == nil {
		return infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "affiliate service unavailable")
	}
	if _, err := s.affiliateService.BindInviter(ctx, userID, inviterID); err != nil {
		return err
	}
	if s.distributionInviteService == nil {
		return nil
	}
	return s.distributionInviteService.ApplyInviteDefaultGroups(ctx, userID, groupIDs)
}

func (s *AuthService) applyOrBindNewUserAffiliate(ctx context.Context, userID int64, affiliateCode string, inviterID int64, groupIDs []int64) error {
	if inviterID > 0 {
		if err := s.applyDistributionInvite(ctx, userID, inviterID, groupIDs); err != nil {
			s.deleteCreatedUserBestEffort(ctx, userID)
			return err
		}
		return nil
	}
	s.bindOAuthAffiliate(ctx, userID, affiliateCode)
	return nil
}

func (s *AuthService) deleteCreatedUserBestEffort(ctx context.Context, userID int64) {
	if s == nil || s.userRepo == nil || userID <= 0 {
		return
	}
	if err := s.userRepo.Delete(ctx, userID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to delete user %d after distribution invite apply failure: %v", userID, err)
	}
}
