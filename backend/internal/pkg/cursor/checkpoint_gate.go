package cursor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

const checkpointStateKeyPrefix = "cursor-checkpoint:v1:"

// Checkpoint replay stays disabled until it passes the real-account quality
// gates. This package only defines opaque state-key helpers; it stores no state.
const checkpointProductionEnabled = false

type CheckpointGateConfig struct {
	Enabled bool
}

func DefaultCheckpointGateConfig() CheckpointGateConfig {
	return CheckpointGateConfig{Enabled: checkpointProductionEnabled}
}

func CheckpointStateKey(accountID int64, conversationID string) (string, error) {
	conversationID = strings.TrimSpace(conversationID)
	if accountID <= 0 {
		return "", errors.New("checkpoint account scope is required")
	}
	if conversationID == "" {
		return "", errors.New("checkpoint conversation scope is required")
	}

	sum := sha256.Sum256([]byte(
		"cursor-checkpoint-state:v1\x00" +
			strconv.FormatInt(accountID, 10) +
			"\x00" +
			conversationID,
	))
	return checkpointStateKeyPrefix + hex.EncodeToString(sum[:]), nil
}

func ValidateCheckpointStateKey(key string) bool {
	if !strings.HasPrefix(key, checkpointStateKeyPrefix) {
		return false
	}
	digest := strings.TrimPrefix(key, checkpointStateKeyPrefix)
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
