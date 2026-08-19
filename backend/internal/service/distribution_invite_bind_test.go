package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestShouldBindAffiliateCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		enabled  bool
		role     string
		status   string
		wantBind bool
	}{
		{"rebate on + regular user", true, RoleUser, StatusActive, true},
		{"rebate on + disabled user", true, RoleUser, StatusDisabled, true},
		{"rebate on + empty owner", true, "", "", true},
		{"rebate on + affiliate_admin", true, RoleAffiliateAdmin, StatusActive, true},
		{"rebate off + active affiliate_admin", false, RoleAffiliateAdmin, StatusActive, true},
		{"rebate off + disabled affiliate_admin", false, RoleAffiliateAdmin, StatusDisabled, false},
		{"rebate off + regular user", false, RoleUser, StatusActive, false},
		{"rebate off + full admin", false, RoleAdmin, StatusActive, false},
		{"rebate off + empty owner", false, "", "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.wantBind, shouldBindAffiliateCode(tc.enabled, tc.role, tc.status))
		})
	}
}

func TestBindInviterByCode_RebateOffAffiliateAdmin(t *testing.T) {
	t.Parallel()

	repo := newDistributionInviteRepoStub(10, RoleAffiliateAdmin, StatusActive)
	svc := &AffiliateService{repo: repo}

	require.NoError(t, svc.BindInviterByCode(context.Background(), 99, "ADM1NCODE"))
	require.Equal(t, [][2]int64{{99, 10}}, repo.bindCalls)
}

func TestBindInviterByCode_RebateOffRegularUser(t *testing.T) {
	t.Parallel()

	repo := newDistributionInviteRepoStub(10, RoleUser, StatusActive)
	svc := &AffiliateService{repo: repo}

	require.NoError(t, svc.BindInviterByCode(context.Background(), 99, "USERCODE1"))
	require.Empty(t, repo.bindCalls)
}

func TestBindInviterByCode_RebateOnRegularUser(t *testing.T) {
	t.Parallel()

	repo := newDistributionInviteRepoStub(10, RoleUser, StatusActive)
	svc := &AffiliateService{
		repo:           repo,
		settingService: NewSettingService(&distributionInviteSettingStub{enabled: true}, nil),
	}

	require.NoError(t, svc.BindInviterByCode(context.Background(), 99, "USERCODE1"))
	require.Equal(t, [][2]int64{{99, 10}}, repo.bindCalls)
}

func TestBindInviterByCode_AlreadyBound(t *testing.T) {
	t.Parallel()

	inviterID := int64(10)
	repo := newDistributionInviteRepoStub(10, RoleAffiliateAdmin, StatusActive)
	repo.self = &AffiliateSummary{UserID: 99, AffCode: "SELFCODE1", InviterID: &inviterID}
	svc := &AffiliateService{repo: repo}

	require.NoError(t, svc.BindInviterByCode(context.Background(), 99, "ADM1NCODE"))
	require.Empty(t, repo.bindCalls)
}

func TestBindInviterByCode_DisabledAffiliateAdmin(t *testing.T) {
	t.Parallel()

	repo := newDistributionInviteRepoStub(10, RoleAffiliateAdmin, StatusDisabled)
	svc := &AffiliateService{repo: repo}

	require.NoError(t, svc.BindInviterByCode(context.Background(), 99, "ADM1NCODE"))
	require.Empty(t, repo.bindCalls)
}

func TestResolveDistributionInviter(t *testing.T) {
	t.Parallel()

	t.Run("active affiliate_admin", func(t *testing.T) {
		t.Parallel()
		svc := &AffiliateService{repo: newDistributionInviteRepoStub(10, RoleAffiliateAdmin, StatusActive)}
		user, err := svc.ResolveDistributionInviter(context.Background(), "ADM1NCODE")
		require.NoError(t, err)
		require.Equal(t, int64(10), user.ID)
		require.Equal(t, RoleAffiliateAdmin, user.Role)
		require.Equal(t, StatusActive, user.Status)
	})

	t.Run("regular user is invalid", func(t *testing.T) {
		t.Parallel()
		svc := &AffiliateService{repo: newDistributionInviteRepoStub(10, RoleUser, StatusActive)}
		user, err := svc.ResolveDistributionInviter(context.Background(), "USERCODE1")
		require.Nil(t, user)
		require.ErrorIs(t, err, ErrDistributionInviteInvalid)
		require.Equal(t, "DISTRIBUTION_INVITE_INVALID", infraerrors.Reason(err))
	})

	t.Run("disabled affiliate_admin", func(t *testing.T) {
		t.Parallel()
		svc := &AffiliateService{repo: newDistributionInviteRepoStub(10, RoleAffiliateAdmin, StatusDisabled)}
		user, err := svc.ResolveDistributionInviter(context.Background(), "ADM1NCODE")
		require.Nil(t, user)
		require.ErrorIs(t, err, ErrDistributionInviteDisabled)
		require.Equal(t, "DISTRIBUTION_INVITE_DISABLED", infraerrors.Reason(err))
	})

	t.Run("unknown code is invalid", func(t *testing.T) {
		t.Parallel()
		svc := &AffiliateService{repo: newDistributionInviteRepoStub(10, RoleAffiliateAdmin, StatusActive)}
		user, err := svc.ResolveDistributionInviter(context.Background(), "NOPECODE1")
		require.Nil(t, user)
		require.ErrorIs(t, err, ErrDistributionInviteInvalid)
	})
}

type distributionInviteSettingStub struct {
	enabled bool
}

func (s *distributionInviteSettingStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *distributionInviteSettingStub) GetValue(_ context.Context, key string) (string, error) {
	if key == SettingKeyAffiliateEnabled && s.enabled {
		return "true", nil
	}
	return "", ErrSettingNotFound
}

func (s *distributionInviteSettingStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

func (s *distributionInviteSettingStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *distributionInviteSettingStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *distributionInviteSettingStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *distributionInviteSettingStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

type distributionInviteRepoStub struct {
	self      *AffiliateSummary
	owner     *AffiliateSummary
	bindCalls [][2]int64
}

func newDistributionInviteRepoStub(ownerID int64, role, status string) *distributionInviteRepoStub {
	return &distributionInviteRepoStub{
		owner: &AffiliateSummary{
			UserID:      ownerID,
			AffCode:     "CODE",
			OwnerRole:   role,
			OwnerStatus: status,
		},
	}
}

func (r *distributionInviteRepoStub) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	if r.self != nil {
		cp := *r.self
		if cp.UserID == 0 {
			cp.UserID = userID
		}
		return &cp, nil
	}
	return &AffiliateSummary{UserID: userID, AffCode: "SELFCODE1"}, nil
}

func (r *distributionInviteRepoStub) GetAffiliateByCode(_ context.Context, code string) (*AffiliateSummary, error) {
	if r.owner == nil {
		return nil, ErrAffiliateProfileNotFound
	}
	switch code {
	case "ADM1NCODE", "USERCODE1":
		cp := *r.owner
		cp.AffCode = code
		return &cp, nil
	default:
		return nil, ErrAffiliateProfileNotFound
	}
}

func (r *distributionInviteRepoStub) BindInviter(_ context.Context, userID, inviterID int64) (bool, error) {
	r.bindCalls = append(r.bindCalls, [2]int64{userID, inviterID})
	return true, nil
}

func (r *distributionInviteRepoStub) AccrueQuota(context.Context, int64, int64, float64, int, *int64) (bool, error) {
	panic("unexpected AccrueQuota call")
}

func (r *distributionInviteRepoStub) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	panic("unexpected GetAccruedRebateFromInvitee call")
}

func (r *distributionInviteRepoStub) ThawFrozenQuota(context.Context, int64) (float64, error) {
	panic("unexpected ThawFrozenQuota call")
}

func (r *distributionInviteRepoStub) TransferQuotaToBalance(context.Context, int64) (float64, float64, error) {
	panic("unexpected TransferQuotaToBalance call")
}

func (r *distributionInviteRepoStub) ListInvitees(context.Context, int64, int) ([]AffiliateInvitee, error) {
	panic("unexpected ListInvitees call")
}

func (r *distributionInviteRepoStub) UpdateUserAffCode(context.Context, int64, string) error {
	panic("unexpected UpdateUserAffCode call")
}

func (r *distributionInviteRepoStub) ResetUserAffCode(context.Context, int64) (string, error) {
	panic("unexpected ResetUserAffCode call")
}

func (r *distributionInviteRepoStub) SetUserRebateRate(context.Context, int64, *float64) error {
	panic("unexpected SetUserRebateRate call")
}

func (r *distributionInviteRepoStub) BatchSetUserRebateRate(context.Context, []int64, *float64) error {
	panic("unexpected BatchSetUserRebateRate call")
}

func (r *distributionInviteRepoStub) ListUsersWithCustomSettings(context.Context, AffiliateAdminFilter) ([]AffiliateAdminEntry, int64, error) {
	panic("unexpected ListUsersWithCustomSettings call")
}

func (r *distributionInviteRepoStub) ListAffiliateInviteRecords(context.Context, AffiliateRecordFilter) ([]AffiliateInviteRecord, int64, error) {
	panic("unexpected ListAffiliateInviteRecords call")
}

func (r *distributionInviteRepoStub) ListAffiliateRebateRecords(context.Context, AffiliateRecordFilter) ([]AffiliateRebateRecord, int64, error) {
	panic("unexpected ListAffiliateRebateRecords call")
}

func (r *distributionInviteRepoStub) ListAffiliateTransferRecords(context.Context, AffiliateRecordFilter) ([]AffiliateTransferRecord, int64, error) {
	panic("unexpected ListAffiliateTransferRecords call")
}

func (r *distributionInviteRepoStub) GetAffiliateUserOverview(context.Context, int64) (*AffiliateUserOverview, error) {
	panic("unexpected GetAffiliateUserOverview call")
}

var _ AffiliateRepository = (*distributionInviteRepoStub)(nil)
