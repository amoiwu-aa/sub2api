//go:build unit

package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserFromServiceDoesNotExposeAdministratorNotes(t *testing.T) {
	user := &service.User{
		ID:       42,
		Email:    "user@example.com",
		Username: "user",
		Role:     service.RoleUser,
		Status:   service.StatusActive,
		Notes:    "internal administrator note",
	}

	payload, err := json.Marshal(UserFromService(user))
	require.NoError(t, err)
	require.NotContains(t, string(payload), `"notes"`)
	require.NotContains(t, string(payload), user.Notes)

	adminPayload, err := json.Marshal(UserFromServiceAdmin(user))
	require.NoError(t, err)
	require.Contains(t, string(adminPayload), `"notes":"internal administrator note"`)
}

func TestRedeemCodeFromServiceDoesNotExposeAdministratorAdjustmentNotes(t *testing.T) {
	code := &service.RedeemCode{
		ID:     9,
		Code:   "ADMIN-ADJUSTMENT",
		Type:   "admin_balance",
		Status: service.StatusUsed,
		Notes:  "internal balance adjustment reason",
	}

	payload, err := json.Marshal(RedeemCodeFromService(code))
	require.NoError(t, err)
	require.NotContains(t, string(payload), `"notes"`)
	require.NotContains(t, string(payload), code.Notes)

	adminPayload, err := json.Marshal(RedeemCodeFromServiceAdmin(code))
	require.NoError(t, err)
	require.Contains(t, string(adminPayload), `"notes":"internal balance adjustment reason"`)
}
