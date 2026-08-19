package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type expiringBalanceRedeemRepoStub struct {
	redeemRejectRepo
	created []RedeemCode
}

func (s *expiringBalanceRedeemRepoStub) Create(_ context.Context, code *RedeemCode) error {
	s.created = append(s.created, *code)
	return nil
}

func TestAdminServiceGenerateRedeemCodes_PreservesBalanceValidity(t *testing.T) {
	repo := &expiringBalanceRedeemRepoStub{}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	codes, err := svc.GenerateRedeemCodes(context.Background(), &GenerateRedeemCodesInput{
		Count:               1,
		Type:                RedeemTypeBalance,
		Value:               10,
		BalanceValidityDays: 7,
	})

	require.NoError(t, err)
	require.Len(t, codes, 1)
	require.Len(t, repo.created, 1)
	require.Equal(t, 7, codes[0].BalanceValidityDays)
	require.Equal(t, 7, repo.created[0].BalanceValidityDays)
}

func TestAdminServiceGenerateRedeemCodes_RejectsBalanceValidityForOtherTypes(t *testing.T) {
	repo := &expiringBalanceRedeemRepoStub{}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	_, err := svc.GenerateRedeemCodes(context.Background(), &GenerateRedeemCodesInput{
		Count:               1,
		Type:                RedeemTypeConcurrency,
		Value:               1,
		BalanceValidityDays: 7,
	})

	require.ErrorContains(t, err, "requires a positive balance redeem code")
	require.Empty(t, repo.created)
}
