package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

// errDistributionGroupNotAllowed is returned when a distribution admin
// assigns a group outside their current catalog.
func errDistributionGroupNotAllowed() error {
	return infraerrors.Forbidden("DISTRIBUTION_GROUP_NOT_ALLOWED", "requested groups are outside the distribution catalog")
}

func allowedGroupIDSet(ids []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			set[id] = struct{}{}
		}
	}
	return set
}

// AllowedGroupsAreSubset reports whether every submitted group ID is present
// in the actor's current AllowedGroups. Empty submitted is always a subset.
func AllowedGroupsAreSubset(submitted, allowed []int64) bool {
	if len(submitted) == 0 {
		return true
	}
	allowedSet := allowedGroupIDSet(allowed)
	for _, id := range submitted {
		if id <= 0 {
			return false
		}
		if _, ok := allowedSet[id]; !ok {
			return false
		}
	}
	return true
}

// FilterActiveGroupsByAllowedIDs keeps groups that are both active and in
// allowed. Unauthorized and inactive groups are dropped.
func FilterActiveGroupsByAllowedIDs(groups []Group, allowed []int64) []Group {
	allowedSet := allowedGroupIDSet(allowed)
	if len(allowedSet) == 0 {
		return []Group{}
	}
	out := make([]Group, 0, len(groups))
	for i := range groups {
		g := groups[i]
		if g.ID <= 0 || g.Status != StatusActive {
			continue
		}
		if _, ok := allowedSet[g.ID]; !ok {
			continue
		}
		out = append(out, g)
	}
	return out
}

// RevokedAllowedGroupIDs returns IDs present in previous but not in next.
func RevokedAllowedGroupIDs(previous, next []int64) []int64 {
	nextSet := allowedGroupIDSet(next)
	seen := make(map[int64]struct{})
	revoked := make([]int64, 0)
	for _, id := range previous {
		if id <= 0 {
			continue
		}
		if _, kept := nextSet[id]; kept {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		revoked = append(revoked, id)
	}
	return revoked
}

// GroupIsPublicUnrestricted is the catalog hint for a public standard group.
// The HTTP catalog already exposes this via is_exclusive + subscription_type;
// frontend can derive the same check without a new DTO field.
func GroupIsPublicUnrestricted(g Group) bool {
	return !g.IsExclusive && g.SubscriptionType == SubscriptionTypeStandard
}
