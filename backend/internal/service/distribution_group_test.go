package service

import (
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestDistributionGroupAllowedGroupsAreSubset(t *testing.T) {
	t.Parallel()

	require.True(t, AllowedGroupsAreSubset(nil, []int64{1, 2}))
	require.True(t, AllowedGroupsAreSubset([]int64{}, []int64{1}))
	require.True(t, AllowedGroupsAreSubset([]int64{2, 1}, []int64{1, 2, 3}))
	require.True(t, AllowedGroupsAreSubset([]int64{1, 1}, []int64{1}))
	require.False(t, AllowedGroupsAreSubset([]int64{4}, []int64{1, 2, 3}))
	require.False(t, AllowedGroupsAreSubset([]int64{1, 9}, []int64{1, 2}))
	require.False(t, AllowedGroupsAreSubset([]int64{1}, nil))
	require.False(t, AllowedGroupsAreSubset([]int64{0}, []int64{0, 1}))
	require.False(t, AllowedGroupsAreSubset([]int64{-1}, []int64{1}))
}

func TestDistributionGroupFilterActiveGroupsByAllowedIDs(t *testing.T) {
	t.Parallel()

	groups := []Group{
		{ID: 1, Name: "allowed-active", Status: StatusActive, IsExclusive: true, SubscriptionType: SubscriptionTypeStandard},
		{ID: 2, Name: "not-allowed", Status: StatusActive, SubscriptionType: SubscriptionTypeStandard},
		{ID: 3, Name: "allowed-inactive", Status: "inactive", IsExclusive: false, SubscriptionType: SubscriptionTypeStandard},
		{ID: 4, Name: "allowed-active-public", Status: StatusActive, IsExclusive: false, SubscriptionType: SubscriptionTypeStandard},
		{ID: 0, Name: "invalid", Status: StatusActive},
	}

	filtered := FilterActiveGroupsByAllowedIDs(groups, []int64{1, 3, 4})
	require.Equal(t, []int64{1, 4}, groupIDs(filtered))
	require.True(t, GroupIsPublicUnrestricted(filtered[1]))
	require.False(t, GroupIsPublicUnrestricted(filtered[0]))

	require.Empty(t, FilterActiveGroupsByAllowedIDs(groups, nil))
	require.Empty(t, FilterActiveGroupsByAllowedIDs(groups, []int64{3}))
}

func TestDistributionGroupRevokedAllowedGroupIDs(t *testing.T) {
	t.Parallel()

	require.Equal(t, []int64{2, 3}, RevokedAllowedGroupIDs([]int64{1, 2, 3}, []int64{1}))
	require.Empty(t, RevokedAllowedGroupIDs([]int64{1, 2}, []int64{2, 1, 3}))
	require.Equal(t, []int64{1}, RevokedAllowedGroupIDs([]int64{1, 1, 0}, nil))
	require.Empty(t, RevokedAllowedGroupIDs(nil, []int64{1}))
}

func TestErrDistributionGroupNotAllowed(t *testing.T) {
	t.Parallel()

	err := errDistributionGroupNotAllowed()
	require.True(t, infraerrors.IsForbidden(err))
	require.Equal(t, "DISTRIBUTION_GROUP_NOT_ALLOWED", infraerrors.Reason(err))
}

func groupIDs(groups []Group) []int64 {
	ids := make([]int64, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	return ids
}
