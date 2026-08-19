package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type affiliateAdminPermissionRepoStub struct {
	allowed       bool
	hasErr        error
	setErr        error
	setAdminID    int64
	setKey        string
	setEnabled    bool
	hasPermission int
}

func (s *affiliateAdminPermissionRepoStub) HasPermission(context.Context, int64, string) (bool, error) {
	s.hasPermission++
	return s.allowed, s.hasErr
}

func (s *affiliateAdminPermissionRepoStub) SetPermission(_ context.Context, adminID int64, key string, enabled bool) error {
	s.setAdminID = adminID
	s.setKey = key
	s.setEnabled = enabled
	return s.setErr
}

type affiliateAdminPermissionUserRepoStub struct {
	UserRepository
	user *User
	err  error
}

func (s *affiliateAdminPermissionUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	return s.user, s.err
}

func TestAffiliateAdminPermissionServiceUpdatesDelegatedPermission(t *testing.T) {
	repo := &affiliateAdminPermissionRepoStub{}
	userRepo := &affiliateAdminPermissionUserRepoStub{
		user: &User{ID: 12, Role: RoleAffiliateAdmin, Status: StatusActive},
	}
	svc := NewAffiliateAdminPermissionService(repo, userRepo)

	err := svc.UpdatePermissions(context.Background(), 12, AffiliateAdminPermissions{
		CanPublishAnnouncements: true,
	})

	require.NoError(t, err)
	require.Equal(t, int64(12), repo.setAdminID)
	require.Equal(t, AffiliateAdminPermissionPublishAnnouncements, repo.setKey)
	require.True(t, repo.setEnabled)
}

func TestAffiliateAdminPermissionServiceRejectsNonAffiliateTarget(t *testing.T) {
	repo := &affiliateAdminPermissionRepoStub{}
	userRepo := &affiliateAdminPermissionUserRepoStub{
		user: &User{ID: 12, Role: RoleUser, Status: StatusActive},
	}
	svc := NewAffiliateAdminPermissionService(repo, userRepo)

	err := svc.UpdatePermissions(context.Background(), 12, AffiliateAdminPermissions{
		CanPublishAnnouncements: true,
	})

	require.ErrorIs(t, err, ErrAffiliateAdminPermissionTarget)
	require.Zero(t, repo.setAdminID)
}

func TestAffiliateAdminPermissionServiceInactiveAffiliateCannotPublish(t *testing.T) {
	repo := &affiliateAdminPermissionRepoStub{allowed: true}
	userRepo := &affiliateAdminPermissionUserRepoStub{
		user: &User{ID: 12, Role: RoleAffiliateAdmin, Status: StatusDisabled},
	}
	svc := NewAffiliateAdminPermissionService(repo, userRepo)

	allowed, err := svc.CanPublishAnnouncements(context.Background(), 12)

	require.NoError(t, err)
	require.False(t, allowed)
	require.Zero(t, repo.hasPermission)
}

func TestAffiliateAdminPermissionServicePropagatesRepositoryFailure(t *testing.T) {
	repoErr := errors.New("permission store unavailable")
	repo := &affiliateAdminPermissionRepoStub{hasErr: repoErr}
	userRepo := &affiliateAdminPermissionUserRepoStub{
		user: &User{ID: 12, Role: RoleAffiliateAdmin, Status: StatusActive},
	}
	svc := NewAffiliateAdminPermissionService(repo, userRepo)

	err := svc.RequirePublishAnnouncements(context.Background(), 12)

	require.ErrorIs(t, err, repoErr)
}
