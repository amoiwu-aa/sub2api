package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type announcementRepoStub struct {
	item        *Announcement
	activeItems []Announcement
}

func (s *announcementRepoStub) Create(_ context.Context, a *Announcement) error {
	s.item = a
	return nil
}

func (s *announcementRepoStub) GetByID(_ context.Context, _ int64) (*Announcement, error) {
	if s.item == nil {
		return nil, ErrAnnouncementNotFound
	}
	return s.item, nil
}

func (s *announcementRepoStub) Update(_ context.Context, a *Announcement) error {
	s.item = a
	return nil
}

func (*announcementRepoStub) Delete(context.Context, int64) error {
	return nil
}

func (*announcementRepoStub) List(context.Context, pagination.PaginationParams, AnnouncementListFilters) ([]Announcement, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (s *announcementRepoStub) ListActive(context.Context, time.Time) ([]Announcement, error) {
	return s.activeItems, nil
}

type scopedAnnouncementUserRepoStub struct {
	UserRepository
	user           *User
	managed        bool
	checkedUserID  int64
	checkedAdminID int64
}

func (s *scopedAnnouncementUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	return s.user, nil
}

func (s *scopedAnnouncementUserRepoStub) UserIsManagedBy(_ context.Context, userID, adminID int64) (bool, error) {
	s.checkedUserID = userID
	s.checkedAdminID = adminID
	return s.managed, nil
}

type scopedAnnouncementReadRepoStub struct {
	AnnouncementReadRepository
}

func (*scopedAnnouncementReadRepoStub) GetReadMapByUser(context.Context, int64, []int64) (map[int64]time.Time, error) {
	return map[int64]time.Time{}, nil
}

type scopedAnnouncementSubscriptionRepoStub struct {
	UserSubscriptionRepository
}

func (*scopedAnnouncementSubscriptionRepoStub) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	return []UserSubscription{}, nil
}

func TestAnnouncementServiceCreateRejectsEqualStartEndTimes(t *testing.T) {
	repo := &announcementRepoStub{}
	svc := NewAnnouncementService(repo, nil, nil, nil)
	now := time.Unix(1776790020, 0)

	_, err := svc.Create(context.Background(), &CreateAnnouncementInput{
		Title:      "公告",
		Content:    "内容",
		Status:     AnnouncementStatusActive,
		NotifyMode: AnnouncementNotifyModePopup,
		StartsAt:   &now,
		EndsAt:     &now,
	})
	require.ErrorIs(t, err, ErrAnnouncementInvalidSchedule)
}

func TestAnnouncementServiceUpdateRejectsEqualStartEndTimes(t *testing.T) {
	repo := &announcementRepoStub{
		item: &Announcement{
			ID:         1,
			Title:      "公告",
			Content:    "内容",
			Status:     AnnouncementStatusActive,
			NotifyMode: AnnouncementNotifyModePopup,
		},
	}
	svc := NewAnnouncementService(repo, nil, nil, nil)
	now := time.Unix(1776790020, 0)
	startsAt := &now
	endsAt := &now

	_, err := svc.Update(context.Background(), 1, &UpdateAnnouncementInput{
		StartsAt: &startsAt,
		EndsAt:   &endsAt,
	})
	require.ErrorIs(t, err, ErrAnnouncementInvalidSchedule)
}

func TestAnnouncementServiceListForUserEnforcesAffiliateAudience(t *testing.T) {
	affiliateAdminID := int64(42)
	repo := &announcementRepoStub{
		activeItems: []Announcement{{
			ID:         8,
			Title:      "分销通知",
			Content:    "内容",
			Status:     AnnouncementStatusActive,
			NotifyMode: AnnouncementNotifyModeSilent,
			Targeting: AnnouncementTargeting{
				AffiliateAdminID: &affiliateAdminID,
			},
		}},
	}
	userRepo := &scopedAnnouncementUserRepoStub{
		user:    &User{ID: 7, Role: RoleUser, Status: StatusActive},
		managed: true,
	}
	svc := NewAnnouncementService(
		repo,
		&scopedAnnouncementReadRepoStub{},
		userRepo,
		&scopedAnnouncementSubscriptionRepoStub{},
	)

	items, err := svc.ListForUser(context.Background(), 7, false)

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(7), userRepo.checkedUserID)
	require.Equal(t, affiliateAdminID, userRepo.checkedAdminID)
}

func TestAnnouncementServiceListForUserHidesOtherAffiliateAudience(t *testing.T) {
	affiliateAdminID := int64(42)
	repo := &announcementRepoStub{
		activeItems: []Announcement{{
			ID:         8,
			Title:      "其他分销通知",
			Content:    "内容",
			Status:     AnnouncementStatusActive,
			NotifyMode: AnnouncementNotifyModeSilent,
			Targeting: AnnouncementTargeting{
				AffiliateAdminID: &affiliateAdminID,
			},
		}},
	}
	userRepo := &scopedAnnouncementUserRepoStub{
		user:    &User{ID: 7, Role: RoleUser, Status: StatusActive},
		managed: false,
	}
	svc := NewAnnouncementService(
		repo,
		&scopedAnnouncementReadRepoStub{},
		userRepo,
		&scopedAnnouncementSubscriptionRepoStub{},
	)

	items, err := svc.ListForUser(context.Background(), 7, false)

	require.NoError(t, err)
	require.Empty(t, items)
}
