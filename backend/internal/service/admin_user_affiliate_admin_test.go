//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type managedUserRepoStub struct {
	*userRepoStub
	managedAdminID int64
	managedErr     error
}

func (s *managedUserRepoStub) CreateManagedUser(ctx context.Context, user *User, adminID int64) error {
	s.managedAdminID = adminID
	if s.managedErr != nil {
		return s.managedErr
	}
	return s.Create(ctx, user)
}

type ownershipUserRepoStub struct {
	*userRepoStub
	managed      map[[2]int64]bool
	managedIDs   []int64
	removedPairs [][2]int64
}

func (s *ownershipUserRepoStub) UserIsManagedBy(_ context.Context, userID, adminID int64) (bool, error) {
	return s.managed[[2]int64{userID, adminID}], nil
}

func (s *ownershipUserRepoStub) ListManagedUserIDs(_ context.Context, _ int64, _ bool) ([]int64, error) {
	return append([]int64(nil), s.managedIDs...), nil
}

func (s *ownershipUserRepoStub) RemoveGroupFromUserAllowedGroups(_ context.Context, userID, groupID int64) error {
	s.removedPairs = append(s.removedPairs, [2]int64{userID, groupID})
	return nil
}

func affiliateActorWithGroups(id int64, groups []int64) *User {
	return &User{
		ID:            id,
		Email:         "aff-actor@test.com",
		Role:          RoleAffiliateAdmin,
		Status:        StatusActive,
		AllowedGroups: groups,
	}
}

func TestAdminService_CreateUser_AffiliateAdminForcesUserAndBinds(t *testing.T) {
	repo := &managedUserRepoStub{userRepoStub: &userRepoStub{
		nextID:    88,
		usersByID: map[int64]*User{7: affiliateActorWithGroups(7, []int64{4})},
	}}
	cfg := &config.Config{
		Default: config.DefaultConfig{
			UserBalance:     0,
			UserConcurrency: 2,
		},
	}
	settingService := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyDefaultBalance:     "1.5",
		SettingKeyDefaultConcurrency: "3",
	}}, cfg)
	svc := &adminServiceImpl{
		userRepo:       repo,
		settingService: settingService,
	}
	balance := 99.0

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:         "owned@test.com",
		Password:      "strong-pass",
		Role:          RoleAdmin,
		Balance:       &balance,
		Concurrency:   9,
		RPMLimit:      40,
		AllowedGroups: []int64{4},
		ActorAdminID:  7,
		ActorRole:     RoleAffiliateAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, RoleUser, user.Role)
	require.Equal(t, 1.5, user.Balance)
	require.Equal(t, 3, user.Concurrency)
	require.Zero(t, user.RPMLimit)
	require.Equal(t, []int64{4}, user.AllowedGroups)
	require.NotNil(t, user.CreatedByAdminID)
	require.Equal(t, int64(7), *user.CreatedByAdminID)
	require.Equal(t, int64(7), repo.managedAdminID)
	require.Len(t, repo.created, 1)
	require.Equal(t, int64(7), *repo.created[0].CreatedByAdminID)
}

func TestAdminService_CreateUser_AffiliateAdminFailsClosedWithoutAtomicCreator(t *testing.T) {
	repo := &userRepoStub{
		nextID:    88,
		usersByID: map[int64]*User{7: affiliateActorWithGroups(7, []int64{1})},
	}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:        "owned@test.com",
		Password:     "strong-pass",
		ActorAdminID: 7,
		ActorRole:    RoleAffiliateAdmin,
	})

	require.ErrorContains(t, err, "managed user creation is unavailable")
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_AffiliateAdminPropagatesAtomicCreateFailure(t *testing.T) {
	expectedErr := errors.New("bind failed")
	repo := &managedUserRepoStub{
		userRepoStub: &userRepoStub{
			nextID:    88,
			usersByID: map[int64]*User{7: affiliateActorWithGroups(7, []int64{1})},
		},
		managedErr: expectedErr,
	}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:        "owned@test.com",
		Password:     "strong-pass",
		ActorAdminID: 7,
		ActorRole:    RoleAffiliateAdmin,
	})

	require.ErrorIs(t, err, expectedErr)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_SuperAdminCanAssignAffiliateAdmin(t *testing.T) {
	repo := &userRepoStub{nextID: 41}
	svc := &adminServiceImpl{userRepo: repo}

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:        "aff@test.com",
		Password:     "strong-pass",
		Role:         RoleAffiliateAdmin,
		ActorAdminID: 1,
		ActorRole:    RoleAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, RoleAffiliateAdmin, user.Role)
	require.Nil(t, user.CreatedByAdminID)
}

func TestAdminService_UpdateUser_AffiliateAdminStripsPrivilegedFields(t *testing.T) {
	target := &User{
		ID:            11,
		Email:         "owned@test.com",
		Role:          RoleUser,
		Balance:       8,
		Concurrency:   2,
		RPMLimit:      10,
		Status:        StatusActive,
		AllowedGroups: []int64{3},
	}
	base := &userRepoStub{
		user: target,
		usersByID: map[int64]*User{
			7:  affiliateActorWithGroups(7, []int64{3, 8}),
			11: target,
		},
	}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}
	notes := "keep me"
	newBalance := 50.0
	newConcurrency := 9
	newRPM := 99
	groups := []int64{8}

	updated, err := svc.UpdateUser(context.Background(), 11, &UpdateUserInput{
		Email:         "owned@test.com",
		Notes:         &notes,
		Role:          RoleAdmin,
		Balance:       &newBalance,
		Concurrency:   &newConcurrency,
		RPMLimit:      &newRPM,
		AllowedGroups: &groups,
		ActorAdminID:  7,
		ActorRole:     RoleAffiliateAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, RoleUser, updated.Role)
	require.Equal(t, 8.0, updated.Balance)
	require.Equal(t, 2, updated.Concurrency)
	require.Equal(t, 10, updated.RPMLimit)
	require.Equal(t, []int64{8}, updated.AllowedGroups)
	require.Equal(t, "keep me", updated.Notes)
}

func TestAdminService_UpdateUser_SuperAdminCanSetAffiliateAdmin(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 12, Email: "u@test.com", Role: RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: &authCacheInvalidatorStub{},
	}

	updated, err := svc.UpdateUser(context.Background(), 12, &UpdateUserInput{
		Role:         RoleAffiliateAdmin,
		ActorRole:    RoleAdmin,
		ActorAdminID: 1,
	})
	require.NoError(t, err)
	require.Equal(t, RoleAffiliateAdmin, updated.Role)
}

func TestAdminService_UpdateUser_DemoteLastAdminToAffiliateRejected(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "a@example.com", Role: RoleAdmin}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 1}
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: RoleAffiliateAdmin})
	require.Error(t, err)
	require.Contains(t, err.Error(), "last admin")
	require.Nil(t, repo.lastUpdated)
}

func TestAdminService_UserIsManagedBy_UsesOwnershipStore(t *testing.T) {
	repo := &ownershipUserRepoStub{
		userRepoStub: &userRepoStub{},
		managed:      map[[2]int64]bool{{11, 7}: true},
	}
	svc := &adminServiceImpl{userRepo: repo}

	ok, err := svc.UserIsManagedBy(context.Background(), 11, 7)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = svc.UserIsManagedBy(context.Background(), 99, 7)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestAdminService_UserIsManagedBy_FailClosedWithoutStore(t *testing.T) {
	svc := &adminServiceImpl{userRepo: &userRepoStub{}}
	ok, err := svc.UserIsManagedBy(context.Background(), 11, 7)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestUser_CanAccessAdminPanel(t *testing.T) {
	require.True(t, (&User{Role: RoleAdmin}).CanAccessAdminPanel())
	require.True(t, (&User{Role: RoleAffiliateAdmin}).CanAccessAdminPanel())
	require.False(t, (&User{Role: RoleUser}).CanAccessAdminPanel())
	require.False(t, (&User{Role: RoleAffiliateAdmin}).IsAdmin())
	require.True(t, RoleCanAccessAdminPanel(RoleAffiliateAdmin))
	require.False(t, RoleCanAccessAdminPanel(RoleUser))
}

func TestAdminService_CreateUser_AffiliateAdminRejectsUnauthorizedGroups(t *testing.T) {
	repo := &managedUserRepoStub{userRepoStub: &userRepoStub{
		nextID:    88,
		usersByID: map[int64]*User{7: affiliateActorWithGroups(7, []int64{4})},
	}}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:         "owned@test.com",
		Password:      "strong-pass",
		AllowedGroups: []int64{4, 9},
		ActorAdminID:  7,
		ActorRole:     RoleAffiliateAdmin,
	})

	require.True(t, infraerrors.IsForbidden(err))
	require.Equal(t, "DISTRIBUTION_GROUP_NOT_ALLOWED", infraerrors.Reason(err))
	require.Empty(t, repo.created)
	require.Zero(t, repo.managedAdminID)
}

func TestAdminService_UpdateUser_AffiliateAdminRejectsUnauthorizedGroups(t *testing.T) {
	target := &User{
		ID:            11,
		Email:         "owned@test.com",
		Role:          RoleUser,
		Status:        StatusActive,
		AllowedGroups: []int64{3},
	}
	base := &userRepoStub{
		user: target,
		usersByID: map[int64]*User{
			7:  affiliateActorWithGroups(7, []int64{3}),
			11: target,
		},
	}
	svc := &adminServiceImpl{userRepo: &rpmUserRepoStub{userRepoStub: base}, redeemCodeRepo: &redeemRepoStub{}}
	groups := []int64{8}

	_, err := svc.UpdateUser(context.Background(), 11, &UpdateUserInput{
		AllowedGroups: &groups,
		ActorAdminID:  7,
		ActorRole:     RoleAffiliateAdmin,
	})

	require.True(t, infraerrors.IsForbidden(err))
	require.Equal(t, "DISTRIBUTION_GROUP_NOT_ALLOWED", infraerrors.Reason(err))
	require.Empty(t, base.updated)
}

func TestAdminService_UpdateUser_SuperAdminShrinksAffiliateGroupsCascadesToManagedUsers(t *testing.T) {
	target := &User{
		ID:            20,
		Email:         "aff@test.com",
		Role:          RoleAffiliateAdmin,
		Status:        StatusActive,
		AllowedGroups: []int64{1, 2, 3},
	}
	repo := &ownershipUserRepoStub{
		userRepoStub: &userRepoStub{
			user:      target,
			usersByID: map[int64]*User{20: target},
		},
		managedIDs: []int64{11, 12},
	}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: invalidator,
	}
	groups := []int64{1}

	updated, err := svc.UpdateUser(context.Background(), 20, &UpdateUserInput{
		AllowedGroups: &groups,
		ActorAdminID:  1,
		ActorRole:     RoleAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1}, updated.AllowedGroups)
	require.ElementsMatch(t, [][2]int64{{11, 2}, {11, 3}, {12, 2}, {12, 3}}, repo.removedPairs)
	require.ElementsMatch(t, []int64{20, 11, 12, 20}, invalidator.userIDs)
}

func TestAdminService_UpdateUser_AffiliateAdminPromoteAndShrinkCascades(t *testing.T) {
	target := &User{
		ID:            20,
		Email:         "u@test.com",
		Role:          RoleUser,
		Status:        StatusActive,
		AllowedGroups: []int64{4, 5},
	}
	repo := &ownershipUserRepoStub{
		userRepoStub: &userRepoStub{
			user:      target,
			usersByID: map[int64]*User{20: target},
		},
		managedIDs: []int64{11},
	}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: invalidator,
	}
	groups := []int64{4}

	updated, err := svc.UpdateUser(context.Background(), 20, &UpdateUserInput{
		Role:          RoleAffiliateAdmin,
		AllowedGroups: &groups,
		ActorAdminID:  1,
		ActorRole:     RoleAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, RoleAffiliateAdmin, updated.Role)
	require.Equal(t, [][2]int64{{11, 5}}, repo.removedPairs)
	require.Contains(t, invalidator.userIDs, int64(11))
	require.Contains(t, invalidator.userIDs, int64(20))
}

func TestAdminService_UpdateUser_AffiliateAdminRegularUserShrinkDoesNotCascade(t *testing.T) {
	target := &User{
		ID:            11,
		Email:         "user@test.com",
		Role:          RoleUser,
		Status:        StatusActive,
		AllowedGroups: []int64{1, 2},
	}
	repo := &ownershipUserRepoStub{
		userRepoStub: &userRepoStub{
			user:      target,
			usersByID: map[int64]*User{11: target},
		},
		managedIDs: []int64{99},
	}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: &authCacheInvalidatorStub{},
	}
	groups := []int64{1}

	_, err := svc.UpdateUser(context.Background(), 11, &UpdateUserInput{
		AllowedGroups: &groups,
		ActorAdminID:  1,
		ActorRole:     RoleAdmin,
	})
	require.NoError(t, err)
	require.Empty(t, repo.removedPairs)
}
