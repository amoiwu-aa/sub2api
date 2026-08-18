//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
	managed map[[2]int64]bool
}

func (s *ownershipUserRepoStub) UserIsManagedBy(_ context.Context, userID, adminID int64) (bool, error) {
	return s.managed[[2]int64{userID, adminID}], nil
}

func TestAdminService_CreateUser_AffiliateAdminForcesUserAndBinds(t *testing.T) {
	repo := &managedUserRepoStub{userRepoStub: &userRepoStub{nextID: 88}}
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
	repo := &userRepoStub{nextID: 88}
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
		userRepoStub: &userRepoStub{nextID: 88},
		managedErr:   expectedErr,
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
	base := &userRepoStub{user: &User{
		ID:            11,
		Email:         "owned@test.com",
		Role:          RoleUser,
		Balance:       8,
		Concurrency:   2,
		RPMLimit:      10,
		Status:        StatusActive,
		AllowedGroups: []int64{3},
	}}
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
