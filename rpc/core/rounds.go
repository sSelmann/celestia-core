package core

import (
  "context"
  "sync"
  "time"

  cmtquery "github.com/cometbft/cometbft/libs/pubsub/query"
  cmtpubsub "github.com/cometbft/cometbft/libs/pubsub"
  cstypes "github.com/cometbft/cometbft/consensus/types"
  "github.com/cometbft/cometbft/types"
  rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
)

type RoundVote struct {
  Validator string    `json:"validator"`
  Type      string    `json:"type"` // "prevote" | "precommit"
  Timestamp time.Time `json:"ts"`
}

type RoundInfo struct {
  Round      int32       `json:"round"`
  Proposer   string      `json:"proposer"`
  Prevotes   []RoundVote `json:"prevotes"`
  Precommits []RoundVote `json:"precommits"`
}

type HeightRounds struct {
  Height      int64                `json:"height"`
  CommitRound int32                `json:"commit_round"`
  Rounds      map[int32]*RoundInfo `json:"rounds"`
}

type roundsCache struct {
  mu    sync.RWMutex
  order []int64                 
  byH   map[int64]*HeightRounds 
  cap   int                     
}
