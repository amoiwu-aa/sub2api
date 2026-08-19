//go:build unit

package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestPrepareDistributionInvite_RegularInvalidCodeIsNonDistribution(t *testing.T) {
	t.Parallel()

	svc := &AuthService{
		affiliateService: &AffiliateService{
			repo: newDistributionInviteRepoStub(10, RoleUser, StatusActive),
		},
	}

	inviterID, groupIDs, err := svc.prepareDistributionInvite(context.Background(), "USERCODE1")

	require.NoError(t, err)
	require.Zero(t, inviterID)
	require.Nil(t, groupIDs)
}

func TestRegisterWithVerification_DisabledDistributionInviteDoesNotCreateUser(t *testing.T) {
	repo := &userRepoStub{nextID: 5}
	svc := newAuthService(repo, map[string]string{
		SettingKeyRegistrationEnabled: "true",
	}, nil, nil)
	svc.affiliateService = &AffiliateService{
		repo: newDistributionInviteRepoStub(7, RoleAffiliateAdmin, StatusActive),
	}
	svc.SetDistributionInviteService(&DistributionInviteService{
		inviteRepo: &inviteSettingsRepoStub{enabled: false, groups: []int64{1}},
		users: &inviteUserStoreStub{
			users: map[int64]*User{
				7: {ID: 7, Role: RoleAffiliateAdmin, AllowedGroups: []int64{1}},
			},
		},
		groups: &inviteGroupStoreStub{groups: []Group{{ID: 1, Status: StatusActive}}},
	})

	_, _, err := svc.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "ADM1NCODE")

	require.ErrorIs(t, err, ErrDistributionInviteDisabled)
	require.Equal(t, "DISTRIBUTION_INVITE_DISABLED", infraerrors.Reason(err))
	require.Empty(t, repo.created)
	require.Empty(t, repo.deletedIDs)
}

func TestRegisterWithVerification_ValidDistributionInviteAppliesGroups(t *testing.T) {
	repo := &userRepoStub{nextID: 99}
	store := &distInviteApplyStore{
		inviter: &User{ID: 7, Role: RoleAffiliateAdmin, AllowedGroups: []int64{3, 5}},
	}
	affRepo := newDistributionInviteRepoStub(7, RoleAffiliateAdmin, StatusActive)
	svc := newAuthService(repo, map[string]string{
		SettingKeyRegistrationEnabled: "true",
	}, nil, nil)
	svc.affiliateService = &AffiliateService{repo: affRepo}
	svc.SetDistributionInviteService(&DistributionInviteService{
		inviteRepo: &inviteSettingsRepoStub{enabled: true, groups: []int64{3, 5}},
		users:      store,
		groups: &inviteGroupStoreStub{groups: []Group{
			{ID: 3, Status: StatusActive},
			{ID: 5, Status: StatusActive},
		}},
	})

	_, user, err := svc.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "ADM1NCODE")

	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(99), user.ID)
	require.Equal(t, []int64{3, 5}, store.appliedGroups[99])
	require.Equal(t, [][2]int64{{99, 7}}, affRepo.bindCalls)
	require.Empty(t, repo.deletedIDs)
}

type distInviteApplyStore struct {
	inviter       *User
	appliedGroups map[int64][]int64
}

func (s *distInviteApplyStore) GetByID(_ context.Context, id int64) (*User, error) {
	if s.inviter != nil && s.inviter.ID == id {
		cp := *s.inviter
		cp.AllowedGroups = append([]int64(nil), s.inviter.AllowedGroups...)
		return &cp, nil
	}
	return &User{ID: id}, nil
}

func (s *distInviteApplyStore) Update(_ context.Context, user *User, fields UserUpdateFields) error {
	if user == nil || !fields.AllowedGroups {
		return nil
	}
	if s.appliedGroups == nil {
		s.appliedGroups = map[int64][]int64{}
	}
	s.appliedGroups[user.ID] = append([]int64(nil), user.AllowedGroups...)
	return nil
}

func (s *distInviteApplyStore) ListWithFilters(context.Context, pagination.PaginationParams, UserListFilters) ([]User, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}
