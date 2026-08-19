package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestDistributionInviteUpdateSettingsRejectsOutsideCatalog(t *testing.T) {
	t.Parallel()

	invite := &inviteSettingsRepoStub{enabled: true, groups: []int64{1}}
	svc := &DistributionInviteService{
		inviteRepo: invite,
		users: &inviteUserStoreStub{
			users: map[int64]*User{
				7: {ID: 7, Role: RoleAffiliateAdmin, AllowedGroups: []int64{1, 2}},
			},
		},
	}

	outside := []int64{1, 9}
	err := svc.UpdateSettings(context.Background(), 7, nil, &outside)

	require.Error(t, err)
	require.True(t, infraerrors.IsForbidden(err))
	require.Equal(t, "DISTRIBUTION_GROUP_NOT_ALLOWED", infraerrors.Reason(err))
	require.Empty(t, invite.replaceCalls)
}

func TestResolveAndIntersectDefaultGroupsDisabled(t *testing.T) {
	t.Parallel()

	svc := &DistributionInviteService{
		inviteRepo: &inviteSettingsRepoStub{enabled: false, groups: []int64{1, 2}},
		users: &inviteUserStoreStub{
			users: map[int64]*User{
				7: {ID: 7, AllowedGroups: []int64{1, 2}},
			},
		},
		groups: &inviteGroupStoreStub{groups: []Group{{ID: 1, Status: StatusActive}}},
	}

	ids, err := svc.ResolveAndIntersectDefaultGroups(context.Background(), 7)

	require.Nil(t, ids)
	require.ErrorIs(t, err, ErrDistributionInviteDisabled)
	require.Equal(t, "DISTRIBUTION_INVITE_DISABLED", infraerrors.Reason(err))
}

func TestResolveAndIntersectDefaultGroupsDropsRevokedAndInactive(t *testing.T) {
	t.Parallel()

	svc := &DistributionInviteService{
		inviteRepo: &inviteSettingsRepoStub{enabled: true, groups: []int64{10, 20, 30}},
		users: &inviteUserStoreStub{
			users: map[int64]*User{
				7: {ID: 7, AllowedGroups: []int64{10, 20}},
			},
		},
		groups: &inviteGroupStoreStub{groups: []Group{
			{ID: 10, Status: StatusActive},
			{ID: 20, Status: StatusDisabled},
			{ID: 30, Status: StatusActive},
		}},
	}

	ids, err := svc.ResolveAndIntersectDefaultGroups(context.Background(), 7)

	require.NoError(t, err)
	require.Equal(t, []int64{10}, ids)
}

func TestDistributionInviteRotateCode(t *testing.T) {
	t.Parallel()

	svc := &DistributionInviteService{
		inviteRepo: &inviteSettingsRepoStub{enabled: true},
		affiliate:  &inviteAffiliateStub{rotateCode: "NEWRAND12AB"},
	}

	code, err := svc.RotateCode(context.Background(), 7)

	require.NoError(t, err)
	require.Equal(t, "NEWRAND12AB", code)
	require.NotEmpty(t, code)
}

func TestDistributionInviteGetProfileRegisterPath(t *testing.T) {
	t.Parallel()

	svc := &DistributionInviteService{
		inviteRepo: &inviteSettingsRepoStub{enabled: true, groups: []int64{3, 5}},
		users: &inviteUserStoreStub{
			listTotal: 4,
			users: map[int64]*User{
				7: {ID: 7, AllowedGroups: []int64{3, 5}},
			},
		},
		affiliate: &inviteAffiliateStub{code: "ADM1NCODE"},
	}

	profile, err := svc.GetProfile(context.Background(), 7)

	require.NoError(t, err)
	require.True(t, profile.Enabled)
	require.Equal(t, "ADM1NCODE", profile.AffCode)
	require.Equal(t, "/register?aff=ADM1NCODE", profile.RegisterPath)
	require.Equal(t, []int64{3, 5}, profile.DefaultGroupIDs)
	require.Equal(t, int64(4), profile.RegistrationCount)
	require.Equal(t, int64(7), svc.users.(*inviteUserStoreStub).listFilters.ManagedByAdminID)
}

func TestDistributionInviteGetProfileDropsRevokedDefaultGroups(t *testing.T) {
	t.Parallel()

	svc := &DistributionInviteService{
		inviteRepo: &inviteSettingsRepoStub{enabled: true, groups: []int64{3, 5}},
		users: &inviteUserStoreStub{
			listTotal: 1,
			users: map[int64]*User{
				7: {ID: 7, AllowedGroups: []int64{3}},
			},
		},
		affiliate: &inviteAffiliateStub{code: "ADM1NCODE"},
	}

	profile, err := svc.GetProfile(context.Background(), 7)

	require.NoError(t, err)
	require.Equal(t, []int64{3}, profile.DefaultGroupIDs)
}

func TestDistributionInviteApplyDefaultGroups(t *testing.T) {
	t.Parallel()

	store := &inviteUserStoreStub{
		users: map[int64]*User{
			99: {ID: 99, AllowedGroups: nil},
		},
	}
	svc := &DistributionInviteService{users: store}

	require.NoError(t, svc.ApplyInviteDefaultGroups(context.Background(), 99, []int64{3, 5}))
	require.Equal(t, []int64{3, 5}, store.users[99].AllowedGroups)
	require.True(t, store.lastFields.AllowedGroups)
}

type inviteSettingsRepoStub struct {
	enabled      bool
	groups       []int64
	replaceCalls [][]int64
}

func (s *inviteSettingsRepoStub) GetOrCreateSettings(context.Context, int64) (bool, error) {
	return s.enabled, nil
}

func (s *inviteSettingsRepoStub) UpdateEnabled(_ context.Context, _ int64, enabled bool) error {
	s.enabled = enabled
	return nil
}

func (s *inviteSettingsRepoStub) ListDefaultGroupIDs(context.Context, int64) ([]int64, error) {
	return append([]int64(nil), s.groups...), nil
}

func (s *inviteSettingsRepoStub) ReplaceDefaultGroupIDs(_ context.Context, _ int64, groupIDs []int64) error {
	s.groups = append([]int64(nil), groupIDs...)
	s.replaceCalls = append(s.replaceCalls, append([]int64(nil), groupIDs...))
	return nil
}

func (s *inviteSettingsRepoStub) RemoveDefaultGroupID(_ context.Context, _ int64, groupID int64) error {
	kept := make([]int64, 0, len(s.groups))
	for _, id := range s.groups {
		if id != groupID {
			kept = append(kept, id)
		}
	}
	s.groups = kept
	return nil
}

func (s *inviteSettingsRepoStub) RemoveDefaultGroupIDForAdmins(context.Context, []int64, int64) error {
	return nil
}

type inviteUserStoreStub struct {
	users       map[int64]*User
	listUsers   []User
	listTotal   int64
	listFilters UserListFilters
	lastFields  UserUpdateFields
}

func (s *inviteUserStoreStub) GetByID(_ context.Context, id int64) (*User, error) {
	user, ok := s.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *user
	cp.AllowedGroups = append([]int64(nil), user.AllowedGroups...)
	return &cp, nil
}

func (s *inviteUserStoreStub) Update(_ context.Context, user *User, fields UserUpdateFields) error {
	s.lastFields = fields
	if s.users == nil {
		s.users = map[int64]*User{}
	}
	cp := *user
	cp.AllowedGroups = append([]int64(nil), user.AllowedGroups...)
	s.users[user.ID] = &cp
	return nil
}

func (s *inviteUserStoreStub) ListWithFilters(_ context.Context, _ pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error) {
	s.listFilters = filters
	return append([]User(nil), s.listUsers...), &pagination.PaginationResult{Total: s.listTotal}, nil
}

type inviteGroupStoreStub struct {
	groups []Group
}

func (s *inviteGroupStoreStub) ListActive(context.Context) ([]Group, error) {
	return append([]Group(nil), s.groups...), nil
}

type inviteAffiliateStub struct {
	code       string
	rotateCode string
}

func (s *inviteAffiliateStub) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	return &AffiliateSummary{UserID: userID, AffCode: s.code}, nil
}

func (s *inviteAffiliateStub) RotateOwnAffCode(context.Context, int64) (string, error) {
	return s.rotateCode, nil
}
