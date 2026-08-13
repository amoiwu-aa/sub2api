package cursor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultCheckpointGateConfigIsDisabled(t *testing.T) {
	config := DefaultCheckpointGateConfig()
	require.False(t, config.Enabled)
}

func TestCheckpointStateKeyIsDeterministicScopedAndOpaque(t *testing.T) {
	const conversationID = "conversation-plaintext-secret"

	first, err := CheckpointStateKey(41, conversationID)
	require.NoError(t, err)
	second, err := CheckpointStateKey(41, conversationID)
	require.NoError(t, err)
	otherAccount, err := CheckpointStateKey(42, conversationID)
	require.NoError(t, err)
	otherConversation, err := CheckpointStateKey(41, "other-conversation")
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.NotEqual(t, first, otherAccount)
	require.NotEqual(t, first, otherConversation)
	require.NotContains(t, first, conversationID)
	require.True(t, ValidateCheckpointStateKey(first))
}

func TestCheckpointStateKeyRejectsMissingScope(t *testing.T) {
	_, err := CheckpointStateKey(0, "conversation")
	require.Error(t, err)
	_, err = CheckpointStateKey(41, " ")
	require.Error(t, err)
}

func TestValidateCheckpointStateKeyRejectsMalformedValues(t *testing.T) {
	require.False(t, ValidateCheckpointStateKey(""))
	require.False(t, ValidateCheckpointStateKey("cursor-checkpoint:v1:plaintext"))
	require.False(t, ValidateCheckpointStateKey("cursor-checkpoint:v2:"+strings.Repeat("a", 64)))
	require.False(t, ValidateCheckpointStateKey("cursor-checkpoint:v1:"+strings.Repeat("z", 64)))
	require.True(t, ValidateCheckpointStateKey("cursor-checkpoint:v1:"+strings.Repeat("a", 64)))
}
