package cursor

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	sandDashboardServicePath = "/aiserver.v1.DashboardService/"
	sandAgentServicePath     = "/agent.v1.AgentService/"
)

// SandUsageStatus is the Grok Bot weekly usage snapshot returned by
// DashboardService/GetSandUsageStatus.
type SandUsageStatus struct {
	CurrentPeriodStart            time.Time
	NextReset                     time.Time
	UsagePercent                  *float64
	IncludedLimitZero             bool
	AvailableBankedResetCount     int64
	UsesPooledEnterpriseAllowance bool
	HasAvailableUsage             bool
	HasNonZeroIncludedLimit       bool
	SandTrialExpiresAt            *time.Time
	SandTrialCancelable           bool
}

// SandPlanUsage is the billing-cycle plan bucket returned by
// DashboardService/GetCurrentPeriodUsage. Monetary values are cents.
type SandPlanUsage struct {
	TotalSpendCents    float64
	IncludedSpendCents float64
	BonusSpendCents    float64
	RemainingCents     float64
	LimitCents         float64
	AutoPercentUsed    *float64
	APIPercentUsed     *float64
	TotalPercentUsed   *float64
}

// SandSpendLimitUsage is the optional on-demand spend bucket. Monetary values
// are cents. A nil IndividualLimitCents means no finite individual limit.
type SandSpendLimitUsage struct {
	TotalSpendCents      float64
	IndividualLimitCents *float64
	IndividualUsedCents  float64
	IndividualRemaining  float64
	LimitType            string
}

// SandCurrentPeriodUsage is the native bearer-token equivalent of the Cursor
// dashboard usage-summary response.
type SandCurrentPeriodUsage struct {
	BillingCycleStart           time.Time
	BillingCycleEnd             time.Time
	PlanUsage                   *SandPlanUsage
	SpendLimitUsage             *SandSpendLimitUsage
	Enabled                     bool
	DisplayMessage              string
	AutoSelectedDisplayMessage  string
	NamedSelectedDisplayMessage string
	AutoBucketModels            []string
}

// FetchSandUsageStatus fetches Grok Bot's native weekly usage window.
func FetchSandUsageStatus(
	ctx context.Context,
	opts *Options,
	accessToken string,
	machineID string,
	clientVersion string,
	namespace string,
) (*SandUsageStatus, error) {
	body, err := sandDashboardUnary(
		ctx,
		opts,
		accessToken,
		machineID,
		clientVersion,
		namespace,
		"GetSandUsageStatus",
	)
	if err != nil {
		return nil, err
	}
	return decodeSandUsageStatus(body)
}

// FetchSandCurrentPeriodUsage fetches Auto/API and spend details using the
// same bearer token and machine identity as the Grok Bot desktop client.
func FetchSandCurrentPeriodUsage(
	ctx context.Context,
	opts *Options,
	accessToken string,
	machineID string,
	clientVersion string,
	namespace string,
) (*SandCurrentPeriodUsage, error) {
	body, err := sandDashboardUnary(
		ctx,
		opts,
		accessToken,
		machineID,
		clientVersion,
		namespace,
		"GetCurrentPeriodUsage",
	)
	if err != nil {
		return nil, err
	}
	return decodeSandCurrentPeriodUsage(body)
}

func sandDashboardUnary(
	ctx context.Context,
	opts *Options,
	accessToken string,
	machineID string,
	clientVersion string,
	namespace string,
	method string,
) ([]byte, error) {
	return sandConnectUnary(
		ctx,
		opts,
		accessToken,
		machineID,
		clientVersion,
		namespace,
		sandDashboardServicePath,
		method,
		nil,
		true,
	)
}

func sandConnectUnary(
	ctx context.Context,
	opts *Options,
	accessToken string,
	machineID string,
	clientVersion string,
	namespace string,
	servicePath string,
	method string,
	payload []byte,
	ghostMode bool,
) ([]byte, error) {
	client, err := opts.client()
	if err != nil {
		return nil, err
	}
	operation := strings.Trim(servicePath, "/") + "/" + method
	token := strings.TrimSpace(accessToken)
	if token == "" {
		return nil, &HTTPError{
			Status:    http.StatusUnauthorized,
			Operation: operation,
			Body:      "missing access token",
		}
	}
	version := strings.TrimSpace(clientVersion)
	if version == "" {
		version = SandClientVersion
	}
	machine := strings.TrimSpace(machineID)
	if machine == "" {
		machine = DeriveTelemetryIDs(token).MachineID
	}

	req, err := newRequest(
		ctx,
		http.MethodPost,
		AuthHost+servicePath+method,
		payload,
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Accept", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("X-Cursor-Checksum", SandChecksum(machine, timeNow()))
	req.Header.Set("X-Cursor-Client-Type", "sand")
	req.Header.Set("X-Cursor-Client-Version", version)
	req.Header.Set("X-Sand-Box-Namespace", normalizeSandNamespace(namespace))
	req.Header.Set("X-Ghost-Mode", fmt.Sprintf("%t", ghostMode))
	req.Header.Set("X-Request-ID", uuid.NewString())
	req.Header.Set("User-Agent", "Grok Bot/"+version)

	status, body, err := do(client, req)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &HTTPError{
			Status:    status,
			Operation: operation,
			Body:      string(body),
		}
	}
	return body, nil
}

func decodeSandUsageStatus(data []byte) (*SandUsageStatus, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return nil, fmt.Errorf("decode Sand usage status: %w", err)
	}
	status := &SandUsageStatus{}
	if raw, ok := sandMessageField(fields, 1); ok {
		status.CurrentPeriodStart, err = decodeProtoTimestamp(raw)
		if err != nil {
			return nil, fmt.Errorf("decode Sand current period start: %w", err)
		}
	}
	if raw, ok := sandMessageField(fields, 2); ok {
		status.NextReset, err = decodeProtoTimestamp(raw)
		if err != nil {
			return nil, fmt.Errorf("decode Sand next reset: %w", err)
		}
	}
	status.UsagePercent = sandDoubleField(fields, 3)
	status.IncludedLimitZero = sandBoolField(fields, 4)
	if value, ok := sandVarintField(fields, 5); ok {
		status.AvailableBankedResetCount = int64(value)
	}
	status.UsesPooledEnterpriseAllowance = sandBoolField(fields, 6)
	status.HasAvailableUsage = sandBoolField(fields, 7)
	status.HasNonZeroIncludedLimit = sandBoolField(fields, 8)
	if raw, ok := sandMessageField(fields, 10); ok {
		expiresAt, timestampErr := decodeProtoTimestamp(raw)
		if timestampErr != nil {
			return nil, fmt.Errorf("decode Sand trial expiry: %w", timestampErr)
		}
		status.SandTrialExpiresAt = &expiresAt
	}
	status.SandTrialCancelable = sandBoolField(fields, 11)
	return status, nil
}

func decodeSandCurrentPeriodUsage(data []byte) (*SandCurrentPeriodUsage, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return nil, fmt.Errorf("decode Sand current-period usage: %w", err)
	}
	usage := &SandCurrentPeriodUsage{}
	if value, ok := sandVarintField(fields, 1); ok && value > 0 {
		usage.BillingCycleStart = time.UnixMilli(int64(value)).UTC()
	}
	if value, ok := sandVarintField(fields, 2); ok && value > 0 {
		usage.BillingCycleEnd = time.UnixMilli(int64(value)).UTC()
	}
	if raw, ok := sandMessageField(fields, 3); ok {
		usage.PlanUsage, err = decodeSandPlanUsage(raw)
		if err != nil {
			return nil, err
		}
	}
	if raw, ok := sandMessageField(fields, 4); ok {
		usage.SpendLimitUsage, err = decodeSandSpendLimitUsage(raw)
		if err != nil {
			return nil, err
		}
	}
	usage.Enabled = sandBoolField(fields, 6)
	usage.DisplayMessage = FieldString(fields, 7)
	usage.AutoSelectedDisplayMessage = FieldString(fields, 11)
	usage.NamedSelectedDisplayMessage = FieldString(fields, 12)
	for _, field := range fields {
		if field.Number == 13 && field.WireType == wireBytes {
			usage.AutoBucketModels = append(usage.AutoBucketModels, string(field.Bytes))
		}
	}
	return usage, nil
}

func decodeSandPlanUsage(data []byte) (*SandPlanUsage, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return nil, fmt.Errorf("decode Sand plan usage: %w", err)
	}
	usage := &SandPlanUsage{}
	if value, ok := sandVarintField(fields, 1); ok {
		usage.TotalSpendCents = float64(value)
	}
	if value, ok := sandVarintField(fields, 2); ok {
		usage.IncludedSpendCents = float64(value)
	}
	if value, ok := sandVarintField(fields, 3); ok {
		usage.BonusSpendCents = float64(value)
	}
	if value, ok := sandVarintField(fields, 4); ok {
		usage.RemainingCents = float64(value)
	}
	if value, ok := sandVarintField(fields, 5); ok {
		usage.LimitCents = float64(value)
	}
	usage.AutoPercentUsed = sandDoubleField(fields, 12)
	usage.APIPercentUsed = sandDoubleField(fields, 13)
	usage.TotalPercentUsed = sandDoubleField(fields, 14)
	return usage, nil
}

func decodeSandSpendLimitUsage(data []byte) (*SandSpendLimitUsage, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return nil, fmt.Errorf("decode Sand spend-limit usage: %w", err)
	}
	usage := &SandSpendLimitUsage{}
	if value, ok := sandVarintField(fields, 1); ok {
		usage.TotalSpendCents = float64(value)
	}
	if value, ok := sandVarintField(fields, 5); ok {
		limit := float64(value)
		usage.IndividualLimitCents = &limit
	}
	if value, ok := sandVarintField(fields, 6); ok {
		usage.IndividualUsedCents = float64(value)
	}
	if value, ok := sandVarintField(fields, 7); ok {
		usage.IndividualRemaining = float64(value)
	}
	usage.LimitType = FieldString(fields, 8)
	return usage, nil
}

func decodeProtoTimestamp(data []byte) (time.Time, error) {
	fields, err := ReadFields(data)
	if err != nil {
		return time.Time{}, err
	}
	seconds, _ := sandVarintField(fields, 1)
	nanos, _ := sandVarintField(fields, 2)
	if seconds == 0 && nanos == 0 {
		return time.Time{}, nil
	}
	return time.Unix(int64(seconds), int64(nanos)).UTC(), nil
}

func sandVarintField(fields []Field, number int) (uint64, bool) {
	for _, field := range fields {
		if field.Number == number && field.WireType == wireVarint {
			return field.Varint, true
		}
	}
	return 0, false
}

func sandDoubleField(fields []Field, number int) *float64 {
	for _, field := range fields {
		if field.Number == number && field.WireType == wireFixed64 {
			value := math.Float64frombits(field.Varint)
			return &value
		}
	}
	return nil
}

func sandBoolField(fields []Field, number int) bool {
	value, ok := sandVarintField(fields, number)
	return ok && value != 0
}

func sandMessageField(fields []Field, number int) ([]byte, bool) {
	return FieldBytes(fields, number)
}
