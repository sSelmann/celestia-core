package core

import (
	"fmt"
	
	cm "github.com/cometbft/cometbft/consensus"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	"github.com/cometbft/cometbft/types"
)

// Validators gets the validator set at the given block height.
//
// If no height is provided, it will fetch the latest validator set. Note the
// validators are sorted by their voting power - this is the canonical order
// for the validators in the set as used in computing their Merkle root.
//
// More: https://docs.cometbft.com/v0.38.x/rpc/#/Info/validators
func (env *Environment) Validators(
	_ *rpctypes.Context,
	heightPtr *int64,
	pagePtr, perPagePtr *int,
) (*ctypes.ResultValidators, error) {
	// The latest validator that we know is the NextValidator of the last block.
	height, err := env.getHeight(env.latestUncommittedHeight(), heightPtr)
	if err != nil {
		return nil, err
	}

	validators, err := env.StateStore.LoadValidators(height)
	if err != nil {
		return nil, err
	}

	totalCount := len(validators.Validators)
	perPage := env.validatePerPage(perPagePtr)
	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)

	v := validators.Validators[skipCount : skipCount+cmtmath.MinInt(perPage, totalCount-skipCount)]

	return &ctypes.ResultValidators{
		BlockHeight: height,
		Validators:  v,
		Count:       len(v),
		Total:       totalCount,
	}, nil
}

// DumpConsensusState dumps consensus state.
// UNSTABLE
// More: https://docs.cometbft.com/v0.38.x/rpc/#/Info/dump_consensus_state
func (env *Environment) DumpConsensusState(*rpctypes.Context) (*ctypes.ResultDumpConsensusState, error) {
	// Get Peer consensus states.
	peers := env.P2PPeers.Peers().List()
	peerStates := make([]ctypes.PeerStateInfo, len(peers))
	for i, peer := range peers {
		peerState, ok := peer.Get(types.PeerStateKey).(*cm.PeerState)
		if !ok { // peer does not have a state yet
			continue
		}
		peerStateJSON, err := peerState.MarshalJSON()
		if err != nil {
			return nil, err
		}
		peerStates[i] = ctypes.PeerStateInfo{
			// Peer basic info.
			NodeAddress: peer.SocketAddr().String(),
			// Peer consensus state.
			PeerState: peerStateJSON,
		}
	}
	// Get self round state.
	roundState, err := env.ConsensusState.GetRoundStateJSON()
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultDumpConsensusState{
		RoundState: roundState,
		Peers:      peerStates,
	}, nil
}

// ConsensusState returns a concise summary of the consensus state.
// UNSTABLE
// More: https://docs.cometbft.com/v0.38.x/rpc/#/Info/consensus_state
func (env *Environment) GetConsensusState(*rpctypes.Context) (*ctypes.ResultConsensusState, error) {
	// Get self round state.
	bz, err := env.ConsensusState.GetRoundStateSimpleJSON()
	return &ctypes.ResultConsensusState{RoundState: bz}, err
}

// ConsensusParams gets the consensus parameters at the given block height.
// If no height is provided, it will fetch the latest consensus params.
// More: https://docs.cometbft.com/v0.38.x/rpc/#/Info/consensus_params
func (env *Environment) ConsensusParams(
	_ *rpctypes.Context,
	heightPtr *int64,
) (*ctypes.ResultConsensusParams, error) {
	// The latest consensus params that we know is the consensus params after the
	// last block.
	height, err := env.getHeight(env.latestUncommittedHeight(), heightPtr)
	if err != nil {
		return nil, err
	}

	consensusParams, err := env.StateStore.LoadConsensusParams(height)
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultConsensusParams{
		BlockHeight:     height,
		ConsensusParams: consensusParams,
	}, nil
}

// GetProposerByRound returns the proposers for each round in a given block height.
// This function replicates the exact consensus algorithm used during block production
// to determine which validators were assigned as proposers in each round.
func (env *Environment) GetProposerByRound(
	_ *rpctypes.Context,
	heightPtr *int64,
) (*ctypes.ResultProposerByRound, error) {
	height, err := env.getHeight(env.latestUncommittedHeight(), heightPtr)
	if err != nil {
		return nil, err
	}

	// Load the commit for this height to get the actual round information
	commit := env.BlockStore.LoadSeenCommit(height)
	if commit == nil {
		return nil, fmt.Errorf("no commit found for height %d", height)
	}

	// Get the validator set for this height
	validators, err := env.StateStore.LoadValidators(height)
	if err != nil {
		return nil, err
	}

	// Calculate proposers for each round from 0 to the commit round
	// This replicates the exact algorithm used in consensus/state.go:enterNewRound
	var rounds []ctypes.ProposerRoundInfo
	commitRound := commit.Round
	
	for round := int32(0); round <= commitRound; round++ {
		// For each round, we need to calculate the proposer
		// This replicates the logic from consensus/state.go:enterNewRound
		// The key insight is that we need to simulate what happens when we enter each round
		// from the perspective of round 0
		
		// Create a fresh copy for this round calculation
		roundValSet := validators.Copy()
		
		// If this is not round 0, increment proposer priority by the round number
		// This simulates what happens in enterNewRound when cs.rs.Round < round
		if round > 0 {
			roundValSet.IncrementProposerPriority(round)
		}
		
		proposer := roundValSet.GetProposer()
		if proposer == nil {
			break
		}
		
		rounds = append(rounds, ctypes.ProposerRoundInfo{
			Round:           round,
			ProposerAddress: proposer.Address.String(),
		})
	}

	return &ctypes.ResultProposerByRound{
		Height: fmt.Sprintf("%d", height),
		Rounds: rounds,
	}, nil
}