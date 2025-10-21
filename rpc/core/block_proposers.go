package core

import (
	"fmt"

	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	"github.com/cometbft/cometbft/types"
)

// BlockProposers returns the proposers for each round from 0 to the commit round
// for a given block height. This allows historical analysis of which validators
// were the proposer in each round before the block was finally committed.
//
// The proposer for each round is calculated deterministically using the same
// algorithm that the consensus engine uses (IncrementProposerPriority).
//
// Example: If a block was committed at round 2, this will return the proposers
// for rounds 0, 1, and 2.
func (env *Environment) BlockProposers(
	_ *rpctypes.Context,
	heightPtr *int64,
) (*ctypes.ResultBlockProposers, error) {
	// Get the height
	height, err := env.getHeight(env.BlockStore.Height(), heightPtr)
	if err != nil {
		return nil, err
	}

	// Load the block to get the commit information
	block := env.BlockStore.LoadBlock(height)
	if block == nil {
		return nil, fmt.Errorf("block not found at height %d", height)
	}

	// Get the commit round from the last commit (for height > 1)
	// For height 1, there's no previous commit
	var commitRound int32
	if height > 1 {
		// Load the commit for this block (which is in block at height+1)
		// But if this is the latest block, use SeenCommit
		var commit *types.Commit
		if height == env.BlockStore.Height() {
			commit = env.BlockStore.LoadSeenCommit(height)
		} else {
			commit = env.BlockStore.LoadBlockCommit(height)
		}

		if commit == nil {
			return nil, fmt.Errorf("commit not found for block at height %d", height)
		}
		commitRound = commit.Round
	} else {
		// For height 1, commit round is 0
		commitRound = 0
	}

	// Load the validator set for this height
	validatorSet, err := env.StateStore.LoadValidators(height)
	if err != nil {
		return nil, fmt.Errorf("failed to load validators for height %d: %w", height, err)
	}
	if validatorSet == nil {
		return nil, fmt.Errorf("validator set not found for height %d", height)
	}

	// Calculate proposers for each round from 0 to commitRound
	roundProposers := make([]ctypes.RoundProposer, 0, commitRound+1)

	// Load the validator set for the PREVIOUS height to get the base state
	// This is because LoadValidators(height) already increments for the current height
	var baseValidatorSet *types.ValidatorSet
	var err error
	
	if height == 1 {
		// For height 1, use the validator set from height 1 itself
		baseValidatorSet = validatorSet
	} else {
		baseValidatorSet, err = env.StateStore.LoadValidators(height - 1)
		if err != nil {
			return nil, fmt.Errorf("failed to load validators for height %d: %w", height-1, err)
		}
		if baseValidatorSet == nil {
			return nil, fmt.Errorf("validator set not found for height %d", height-1)
		}
	}

	for round := int32(0); round <= commitRound; round++ {
		// Create a copy of the base validator set for this round
		vals := baseValidatorSet.Copy()

		// Increment proposer priority for this round
		// Round 0 uses the base validator set from previous height
		// For round > 0, we need to increment by the round number
		vals.IncrementProposerPriority(round)

		// Get the proposer for this round
		proposer := vals.GetProposer()
		if proposer == nil {
			return nil, fmt.Errorf("no proposer found for round %d at height %d", round, height)
		}

		roundProposers = append(roundProposers, ctypes.RoundProposer{
			Round:          round,
			ProposerAddr:   proposer.Address,
			ProposerPubKey: proposer.PubKey,
		})
	}

	return &ctypes.ResultBlockProposers{
		BlockHeight:    height,
		CommitRound:    commitRound,
		RoundProposers: roundProposers,
	}, nil
}

