package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbm "github.com/cometbft/cometbft-db"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/libs/log"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	sm "github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/state/mocks"
	"github.com/cometbft/cometbft/types"
)

func TestGetProposerByRound(t *testing.T) {
	// Create test validators
	val1 := types.NewValidator(ed25519.GenPrivKey().PubKey(), 1000)
	val2 := types.NewValidator(ed25519.GenPrivKey().PubKey(), 2000)
	val3 := types.NewValidator(ed25519.GenPrivKey().PubKey(), 3000)
	
	validators := types.NewValidatorSet([]*types.Validator{val1, val2, val3})
	
	// Create a test commit with round 2
	commit := &types.Commit{
		Height: 100,
		Round:  2,
		BlockID: types.BlockID{
			Hash: []byte("test-hash"),
		},
		Signatures: []types.CommitSig{
			{BlockIDFlag: types.BlockIDFlagCommit},
			{BlockIDFlag: types.BlockIDFlagCommit},
			{BlockIDFlag: types.BlockIDFlagCommit},
		},
	}
	
	// Create mock block store
	mockBlockStore := &mocks.BlockStore{}
	mockBlockStore.On("LoadSeenCommit", int64(100)).Return(commit)
	mockBlockStore.On("Height").Return(int64(100))
	
	// Create state store
	stateStore := sm.NewStore(dbm.NewMemDB(), sm.StoreOptions{})
	
	// Save validators for height 100
	err := stateStore.SaveValidators(100, validators)
	require.NoError(t, err)
	
	// Create environment
	env := &Environment{
		BlockStore: mockBlockStore,
		StateStore: stateStore,
		Logger:     log.NewNopLogger(),
	}
	
	// Test the function
	height := int64(100)
	result, err := env.GetProposerByRound(&rpctypes.Context{}, &height)
	require.NoError(t, err)
	require.NotNil(t, result)
	
	// Verify the result
	assert.Equal(t, "100", result.Height)
	assert.Len(t, result.Rounds, 3) // rounds 0, 1, 2
	
	// Verify that we get different proposers for different rounds
	// (this depends on the validator set's proposer selection algorithm)
	proposers := make(map[int32]string)
	for _, round := range result.Rounds {
		proposers[round.Round] = round.ProposerAddress
	}
	
	// Each round should have a proposer
	assert.Contains(t, proposers, int32(0))
	assert.Contains(t, proposers, int32(1))
	assert.Contains(t, proposers, int32(2))
	
	// The proposers should be valid validator addresses
	for round, proposerAddr := range proposers {
		assert.NotEmpty(t, proposerAddr, "Round %d should have a proposer", round)
		// Verify the proposer is one of our test validators
		found := false
		for _, val := range validators.Validators {
			if val.Address.String() == proposerAddr {
				found = true
				break
			}
		}
		assert.True(t, found, "Proposer %s for round %d should be a valid validator", proposerAddr, round)
	}
	
	// Verify that round 0 proposer is the same as the initial proposer
	round0Proposer := proposers[0]
	initialProposer := validators.GetProposer()
	assert.Equal(t, initialProposer.Address.String(), round0Proposer, "Round 0 proposer should match initial proposer")
}

func TestGetProposerByRoundNoCommit(t *testing.T) {
	// Create mock block store that returns nil for commit
	mockBlockStore := &mocks.BlockStore{}
	mockBlockStore.On("LoadSeenCommit", int64(100)).Return((*types.Commit)(nil))
	mockBlockStore.On("Height").Return(int64(100))
	
	// Create environment
	env := &Environment{
		BlockStore: mockBlockStore,
		Logger:     log.NewNopLogger(),
	}
	
	// Test the function
	height := int64(100)
	result, err := env.GetProposerByRound(&rpctypes.Context{}, &height)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no commit found for height 100")
}

func TestGetProposerByRoundRound0(t *testing.T) {
	// Create test validators
	val1 := types.NewValidator(ed25519.GenPrivKey().PubKey(), 1000)
	val2 := types.NewValidator(ed25519.GenPrivKey().PubKey(), 2000)
	
	validators := types.NewValidatorSet([]*types.Validator{val1, val2})
	
	// Create a test commit with round 0 (immediate commit)
	commit := &types.Commit{
		Height: 100,
		Round:  0,
		BlockID: types.BlockID{
			Hash: []byte("test-hash"),
		},
		Signatures: []types.CommitSig{
			{BlockIDFlag: types.BlockIDFlagCommit},
			{BlockIDFlag: types.BlockIDFlagCommit},
		},
	}
	
	// Create mock block store
	mockBlockStore := &mocks.BlockStore{}
	mockBlockStore.On("LoadSeenCommit", int64(100)).Return(commit)
	mockBlockStore.On("Height").Return(int64(100))
	
	// Create state store
	stateStore := sm.NewStore(dbm.NewMemDB(), sm.StoreOptions{})
	
	// Save validators for height 100
	err := stateStore.SaveValidators(100, validators)
	require.NoError(t, err)
	
	// Create environment
	env := &Environment{
		BlockStore: mockBlockStore,
		StateStore: stateStore,
		Logger:     log.NewNopLogger(),
	}
	
	// Test the function
	height := int64(100)
	result, err := env.GetProposerByRound(&rpctypes.Context{}, &height)
	require.NoError(t, err)
	require.NotNil(t, result)
	
	// Verify the result
	assert.Equal(t, "100", result.Height)
	assert.Len(t, result.Rounds, 1) // only round 0
	
	// Verify round 0 has a proposer
	assert.Equal(t, int32(0), result.Rounds[0].Round)
	assert.NotEmpty(t, result.Rounds[0].ProposerAddress)
}
