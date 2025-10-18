// rpc/core/consensus_rounds.go
package core

import (
    rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
    "github.com/cometbft/cometbft/internal/roundlog"
)

type RoundInfo struct {
    Round      int32  `json:"round"`
    Proposer   string `json:"proposer"`
    Prevotes   int    `json:"prevotes_count,omitempty"`
    Precommits int    `json:"precommits_count,omitempty"`
}

type ResultConsensusRounds struct {
    Height      int64       `json:"height"`
    CommitRound int32       `json:"commit_round"`
    Rounds      []RoundInfo `json:"rounds"`
}

func (env *Environment) ConsensusRounds(_ *rpctypes.Context, heightPtr *int64) (*ResultConsensusRounds, error) {
    var height int64
    if heightPtr != nil && *heightPtr > 0 {
        height = *heightPtr
    } else {
        height = env.ConsensusState.GetLastHeight()
    }

    rec := roundlog.Default().Get(height)
    if rec == nil {
        return &ResultConsensusRounds{Height: height, Rounds: []RoundInfo{}}, nil
    }
    out := make([]RoundInfo, 0, len(rec.Rounds))
    for _, rr := range rec.Rounds {
        out = append(out, RoundInfo{
            Round:      rr.Round,
            Proposer:   rr.Proposer,
            Prevotes:   rr.Prevotes,
            Precommits: rr.Precommits,
        })
    }
    return &ResultConsensusRounds{
        Height:      rec.Height,
        CommitRound: rec.CommitRound,
        Rounds:      out,
    }, nil
}
