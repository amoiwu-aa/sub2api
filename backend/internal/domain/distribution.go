package domain

// Distribution / affiliate-admin operation error reasons.
const (
	ErrReasonManagedUserNotFound             = "MANAGED_USER_NOT_FOUND"
	ErrReasonInsufficientDistributionBalance = "INSUFFICIENT_DISTRIBUTION_BALANCE"
	ErrReasonInvalidTransferAmount           = "INVALID_TRANSFER_AMOUNT"
	ErrReasonDistributionTransferConflict    = "DISTRIBUTION_TRANSFER_CONFLICT"
	ErrReasonDistributionGroupNotAllowed     = "DISTRIBUTION_GROUP_NOT_ALLOWED"
	ErrReasonDistributionInviteDisabled      = "DISTRIBUTION_INVITE_DISABLED"
	ErrReasonDistributionInviteInvalid       = "DISTRIBUTION_INVITE_INVALID"
)
