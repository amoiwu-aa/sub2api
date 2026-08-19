package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const distributionInviteRegisterPathPrefix = "/register?aff="

// DistributionInviteProfile is the affiliate-admin invite card: toggle, code,
// register path, default groups, and how many managed users they have.
type DistributionInviteProfile struct {
	Enabled           bool    `json:"enabled"`
	AffCode           string  `json:"aff_code"`
	RegisterPath      string  `json:"register_path"`
	DefaultGroupIDs   []int64 `json:"default_group_ids"`
	RegistrationCount int64   `json:"registration_count"`
}

type distributionInviteUserStore interface {
	GetByID(ctx context.Context, id int64) (*User, error)
	Update(ctx context.Context, user *User, fields UserUpdateFields) error
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error)
}

type distributionInviteGroupStore interface {
	ListActive(ctx context.Context) ([]Group, error)
}

type distributionInviteAffiliate interface {
	EnsureUserAffiliate(ctx context.Context, userID int64) (*AffiliateSummary, error)
	RotateOwnAffCode(ctx context.Context, userID int64) (string, error)
}

// DistributionInviteService manages per-admin invite defaults and applies them
// after CreateManagedUser / a later register-bind hook.
type DistributionInviteService struct {
	inviteRepo DistributionInviteRepository
	users      distributionInviteUserStore
	groups     distributionInviteGroupStore
	affiliate  distributionInviteAffiliate
}

func NewDistributionInviteService(
	inviteRepo DistributionInviteRepository,
	userRepo UserRepository,
	groupRepo GroupRepository,
	affiliate *AffiliateService,
) *DistributionInviteService {
	return &DistributionInviteService{
		inviteRepo: inviteRepo,
		users:      userRepo,
		groups:     groupRepo,
		affiliate:  affiliate,
	}
}

func (s *DistributionInviteService) GetProfile(ctx context.Context, adminID int64) (*DistributionInviteProfile, error) {
	if err := s.requireReady(adminID); err != nil {
		return nil, err
	}
	if s.users == nil || s.affiliate == nil {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable")
	}

	enabled, err := s.inviteRepo.GetOrCreateSettings(ctx, adminID)
	if err != nil {
		return nil, err
	}
	defaultGroupIDs, err := s.inviteRepo.ListDefaultGroupIDs(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if defaultGroupIDs == nil {
		defaultGroupIDs = []int64{}
	}

	summary, err := s.affiliate.EnsureUserAffiliate(ctx, adminID)
	if err != nil {
		return nil, err
	}
	code := ""
	if summary != nil {
		code = summary.AffCode
	}

	count, err := s.registrationCount(ctx, adminID)
	if err != nil {
		return nil, err
	}

	admin, err := s.users.GetByID(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if admin != nil {
		defaultGroupIDs = intersectPositiveIDs(defaultGroupIDs, admin.AllowedGroups)
	} else {
		defaultGroupIDs = []int64{}
	}

	return &DistributionInviteProfile{
		Enabled:           enabled,
		AffCode:           code,
		RegisterPath:      distributionInviteRegisterPath(code),
		DefaultGroupIDs:   defaultGroupIDs,
		RegistrationCount: count,
	}, nil
}

// RemoveDefaultGroupID drops a revoked group from this admin's invite defaults.
func (s *DistributionInviteService) RemoveDefaultGroupID(ctx context.Context, adminID, groupID int64) error {
	if s == nil || s.inviteRepo == nil || adminID <= 0 || groupID <= 0 {
		return nil
	}
	return s.inviteRepo.RemoveDefaultGroupID(ctx, adminID, groupID)
}

func (s *DistributionInviteService) UpdateSettings(ctx context.Context, adminID int64, enabled *bool, defaultGroupIDs *[]int64) error {
	if err := s.requireReady(adminID); err != nil {
		return err
	}
	if defaultGroupIDs != nil {
		if s.users == nil {
			return infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable")
		}
		admin, err := s.users.GetByID(ctx, adminID)
		if err != nil {
			return err
		}
		var catalog []int64
		if admin != nil {
			catalog = admin.AllowedGroups
		}
		if !AllowedGroupsAreSubset(*defaultGroupIDs, catalog) {
			return errDistributionGroupNotAllowed()
		}
	}
	if enabled != nil {
		if err := s.inviteRepo.UpdateEnabled(ctx, adminID, *enabled); err != nil {
			return err
		}
	}
	if defaultGroupIDs != nil {
		if err := s.inviteRepo.ReplaceDefaultGroupIDs(ctx, adminID, *defaultGroupIDs); err != nil {
			return err
		}
	}
	return nil
}

func (s *DistributionInviteService) RotateCode(ctx context.Context, adminID int64) (string, error) {
	if err := s.requireReady(adminID); err != nil {
		return "", err
	}
	if s.affiliate == nil {
		return "", infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable")
	}
	return s.affiliate.RotateOwnAffCode(ctx, adminID)
}

func (s *DistributionInviteService) ListRegistrations(ctx context.Context, adminID int64, page, pageSize int) ([]User, int64, error) {
	if err := s.requireReady(adminID); err != nil {
		return nil, 0, err
	}
	if s.users == nil {
		return nil, 0, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable")
	}
	users, result, err := s.users.ListWithFilters(ctx, registrationListParams(page, pageSize), managedRegistrationFilters(adminID))
	if err != nil {
		return nil, 0, err
	}
	total := int64(0)
	if result != nil {
		total = result.Total
	}
	if users == nil {
		users = []User{}
	}
	return users, total, nil
}

// ResolveAndIntersectDefaultGroups returns default groups ∩ inviter.AllowedGroups
// ∩ currently active groups. Auth can call this later inside register bind.
func (s *DistributionInviteService) ResolveAndIntersectDefaultGroups(ctx context.Context, inviterID int64) ([]int64, error) {
	if err := s.requireReady(inviterID); err != nil {
		return nil, err
	}
	if s.users == nil || s.groups == nil {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable")
	}

	enabled, err := s.inviteRepo.GetOrCreateSettings(ctx, inviterID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrDistributionInviteDisabled
	}

	defaults, err := s.inviteRepo.ListDefaultGroupIDs(ctx, inviterID)
	if err != nil {
		return nil, err
	}
	inviter, err := s.users.GetByID(ctx, inviterID)
	if err != nil {
		return nil, err
	}
	var catalog []int64
	if inviter != nil {
		catalog = inviter.AllowedGroups
	}
	active, err := s.groups.ListActive(ctx)
	if err != nil {
		return nil, err
	}

	allowedDefaults := intersectPositiveIDs(defaults, catalog)
	filtered := FilterActiveGroupsByAllowedIDs(active, allowedDefaults)
	ids := make([]int64, 0, len(filtered))
	for i := range filtered {
		ids = append(ids, filtered[i].ID)
	}
	return ids, nil
}

// ApplyInviteDefaultGroups syncs allowed_groups on a newly created user.
// Call after CreateManagedUser / register bind with IDs from
// ResolveAndIntersectDefaultGroups.
func (s *DistributionInviteService) ApplyInviteDefaultGroups(ctx context.Context, newUserID int64, groupIDs []int64) error {
	if s == nil || s.users == nil {
		return infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable")
	}
	if newUserID <= 0 {
		return infraerrors.BadRequest("INVALID_USER", "invalid user")
	}
	user, err := s.users.GetByID(ctx, newUserID)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserNotFound
	}
	user.AllowedGroups = append([]int64(nil), groupIDs...)
	return s.users.Update(ctx, user, UserUpdateFields{AllowedGroups: true})
}

func (s *DistributionInviteService) requireReady(adminID int64) error {
	if adminID <= 0 {
		return infraerrors.BadRequest("INVALID_USER", "invalid user")
	}
	if s == nil || s.inviteRepo == nil {
		return infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "distribution invite service unavailable")
	}
	return nil
}

func (s *DistributionInviteService) registrationCount(ctx context.Context, adminID int64) (int64, error) {
	_, result, err := s.users.ListWithFilters(ctx, registrationListParams(1, 1), managedRegistrationFilters(adminID))
	if err != nil {
		return 0, err
	}
	if result == nil {
		return 0, nil
	}
	return result.Total, nil
}

func managedRegistrationFilters(adminID int64) UserListFilters {
	includeSubs := false
	return UserListFilters{
		ManagedByAdminID:     adminID,
		IncludeSubscriptions: &includeSubs,
	}
}

func registrationListParams(page, pageSize int) pagination.PaginationParams {
	return pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    "created_at",
		SortOrder: pagination.SortOrderDesc,
	}
}

func distributionInviteRegisterPath(code string) string {
	return distributionInviteRegisterPathPrefix + code
}

func intersectPositiveIDs(left, right []int64) []int64 {
	if len(left) == 0 || len(right) == 0 {
		return []int64{}
	}
	allowed := allowedGroupIDSet(right)
	out := make([]int64, 0, len(left))
	seen := make(map[int64]struct{}, len(left))
	for _, id := range left {
		if id <= 0 {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
