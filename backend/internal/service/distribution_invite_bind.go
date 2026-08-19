package service

import (
	"context"
	"errors"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrDistributionInviteInvalid  = infraerrors.BadRequest("DISTRIBUTION_INVITE_INVALID", "invalid distribution invite code")
	ErrDistributionInviteDisabled = infraerrors.Forbidden("DISTRIBUTION_INVITE_DISABLED", "distribution invite owner is disabled")
)

// shouldBindAffiliateCode decides whether BindInviterByCode should attach an
// inviter. Rebate-on keeps the existing "any valid inviter" rule. Rebate-off
// only binds an active affiliate_admin so 分销归属 still works without accruing rebate.
func shouldBindAffiliateCode(enabled bool, inviterRole, inviterStatus string) bool {
	if enabled {
		return true
	}
	return inviterRole == RoleAffiliateAdmin && inviterStatus == StatusActive
}

// ResolveDistributionInviter looks up aff_code and returns the owner only when
// they are an active affiliate_admin. Callers (e.g. a later transactional
// register) can map the typed errors to HTTP/API reasons.
func (s *AffiliateService) ResolveDistributionInviter(ctx context.Context, rawCode string) (*User, error) {
	if s == nil || s.repo == nil {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "affiliate service unavailable")
	}
	code := strings.ToUpper(strings.TrimSpace(rawCode))
	if !isValidAffiliateCodeFormat(code) {
		return nil, ErrDistributionInviteInvalid
	}

	summary, err := s.repo.GetAffiliateByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrAffiliateProfileNotFound) {
			return nil, ErrDistributionInviteInvalid
		}
		return nil, err
	}
	if summary == nil || summary.UserID <= 0 {
		return nil, ErrDistributionInviteInvalid
	}
	if summary.OwnerRole != RoleAffiliateAdmin {
		return nil, ErrDistributionInviteInvalid
	}
	if summary.OwnerStatus != StatusActive {
		return nil, ErrDistributionInviteDisabled
	}
	return &User{
		ID:     summary.UserID,
		Role:   summary.OwnerRole,
		Status: summary.OwnerStatus,
	}, nil
}
