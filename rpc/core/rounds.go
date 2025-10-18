// rpc/core/rounds.go
package core

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	cmtquery "github.com/cometbft/cometbft/libs/pubsub/query"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
)

// -------------------- DATA MODEL --------------------

type RoundVote struct {
	Validator string    `json:"validator"`
	Type      string    `json:"type"` // "prevote" | "precommit"
	Timestamp time.Time `json:"ts"`
	BlockID   string    `json:"block_id,omitempty"`
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
	env   *Environment // validator set erişimi için
}

func (c *roundsCache) getOrCreate(H int64, R int32) *RoundInfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	hr, ok := c.byH[H]
	if !ok {
		hr = &HeightRounds{Height: H, Rounds: make(map[int32]*RoundInfo)}
		c.byH[H] = hr
		c.order = append(c.order, H)
		c.evictIfNeeded()
	}

	ri, ok := hr.Rounds[R]
	if !ok {
		ri = &RoundInfo{Round: R}
		hr.Rounds[R] = ri
		
		// Proposer'ı validator set'inden hesapla
		if c.env != nil && c.env.StateStore != nil {
			proposer := c.calculateProposer(H, R)
			if proposer != "" {
				ri.Proposer = proposer
			}
		}
	}
	return ri
}

// ValidatorSet'ten proposer'ı hesapla
func (c *roundsCache) calculateProposer(height int64, round int32) string {
	// StateStore'dan validator set'i al
	valSet, err := c.env.StateStore.LoadValidators(height)
	if err != nil || valSet == nil {
		return ""
	}

	// Round kadar proposer priority'yi ilerlet
	// NOT: Copy kullanmalıyız çünkü orijinal set'i değiştirmek istemiyoruz
	valSetCopy := valSet.Copy()
	
	if round > 0 {
		valSetCopy.IncrementProposerPriority(round)
	}
	
	proposer := valSetCopy.GetProposer()
	if proposer == nil {
		return ""
	}
	
	return proposer.Address.String()
}

func (c *roundsCache) evictIfNeeded() {
	for len(c.order) > c.cap {
		oldH := c.order[0]
		c.order = c.order[1:]
		delete(c.byH, oldH)
	}
}

func (c *roundsCache) onNewRound(ev types.EventDataNewRound) {
	ri := c.getOrCreate(ev.Height, ev.Round)
	// NewRound event'inden gelen proposer bilgisi varsa kullan
	if ev.Proposer.Address != nil && len(ev.Proposer.Address) > 0 {
		ri.Proposer = ev.Proposer.Address.String()
	}
}

func (c *roundsCache) onVote(ev types.EventDataVote) {
	if ev.Vote == nil {
		return
	}
	
	rt := "prevote"
	if ev.Vote.Type == cmtproto.PrecommitType {
		rt = "precommit"
	}
	
	ri := c.getOrCreate(ev.Vote.Height, ev.Vote.Round)
	
	blockIDStr := ""
	if !ev.Vote.BlockID.IsZero() {
		blockIDStr = ev.Vote.BlockID.Hash.String()
	}
	
	r := RoundVote{
		Validator: ev.Vote.ValidatorAddress.String(),
		Type:      rt,
		Timestamp: ev.Vote.Timestamp,
		BlockID:   blockIDStr,
	}
	
	if rt == "prevote" {
		ri.Prevotes = append(ri.Prevotes, r)
	} else {
		ri.Precommits = append(ri.Precommits, r)
	}
	
	// Fallback: Eğer proposer yoksa ve bu ilk non-nil prevote ise
	if ri.Proposer == "" && rt == "prevote" && blockIDStr != "" {
		ri.Proposer = ev.Vote.ValidatorAddress.String()
	}
}

func (c *roundsCache) onCompleteProposal(ev types.EventDataCompleteProposal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	hr, ok := c.byH[ev.Height]
	if !ok {
		hr = &HeightRounds{Height: ev.Height, Rounds: make(map[int32]*RoundInfo)}
		c.byH[ev.Height] = hr
		c.order = append(c.order, ev.Height)
		c.evictIfNeeded()
	}
	hr.CommitRound = ev.Round
	
	// Round kaydını oluştur
	if _, ok := hr.Rounds[ev.Round]; !ok {
		ri := &RoundInfo{Round: ev.Round}
		hr.Rounds[ev.Round] = ri
		
		// Proposer'ı hesapla
		if c.env != nil && c.env.StateStore != nil {
			proposer := c.calculateProposer(ev.Height, ev.Round)
			if proposer != "" {
				ri.Proposer = proposer
			}
		}
	}
}

func (c *roundsCache) onRoundState(ev types.EventDataRoundState) {
	// Her round değişiminde kayıt oluştur
	_ = c.getOrCreate(ev.Height, ev.Round)
}

// -------------------- OBSERVER INIT --------------------

func (env *Environment) InitConsensusRoundsObserver(size int) error {
	if size <= 0 {
		size = 10000
	}
	env.rounds = &roundsCache{
		byH: make(map[int64]*HeightRounds),
		cap: size,
		env: env,
	}

	ctx := context.Background()

	// 1) NewRound
	subNR, err := env.EventBus.Subscribe(ctx, "rounds-observer-newround", 
		types.EventQueryNewRound)
	if err != nil {
		return err
	}

	// 2) Vote - en güvenilir veri kaynağı
	subV, err := env.EventBus.Subscribe(ctx, "rounds-observer-vote", 
		types.EventQueryVote)
	if err != nil {
		return err
	}

	// 3) CompleteProposal
	qCP, _ := cmtquery.New(fmt.Sprintf("%s='%s'", types.EventTypeKey, types.EventCompleteProposal))
	subCP, err := env.EventBus.Subscribe(ctx, "rounds-observer-completeproposal", qCP)
	if err != nil {
		return err
	}

	// 4) NewRoundStep
	qStep, _ := cmtquery.New(fmt.Sprintf("%s='%s'", types.EventTypeKey, types.EventNewRoundStep))
	subStep, err := env.EventBus.Subscribe(ctx, "rounds-observer-step", qStep)
	if err != nil {
		subStep = nil
	}

	// Consumer goroutines
	go func() {
		for msg := range subNR.Out() {
			if ev, ok := msg.Data().(types.EventDataNewRound); ok {
				env.rounds.onNewRound(ev)
			}
		}
	}()
	
	go func() {
		for msg := range subV.Out() {
			if ev, ok := msg.Data().(types.EventDataVote); ok {
				env.rounds.onVote(ev)
			}
		}
	}()
	
	go func() {
		for msg := range subCP.Out() {
			if ev, ok := msg.Data().(types.EventDataCompleteProposal); ok {
				env.rounds.onCompleteProposal(ev)
			}
		}
	}()
	
	if subStep != nil {
		go func() {
			for msg := range subStep.Out() {
				if ev, ok := msg.Data().(types.EventDataRoundState); ok {
					env.rounds.onRoundState(ev)
				}
			}
		}()
	}

	return nil
}

// -------------------- RPC HANDLERS --------------------

type ResultConsensusRounds struct {
	Height      int64 `json:"height"`
	CommitRound int32 `json:"commit_round"`
	Rounds      []struct {
		Round           int32  `json:"round"`
		Proposer        string `json:"proposer"`
		PrevotesCount   int    `json:"prevotes_count"`
		PrecommitsCount int    `json:"precommits_count"`
	} `json:"rounds"`
}

func (env *Environment) ConsensusRounds(_ *rpctypes.Context, height int64) (*ResultConsensusRounds, error) {
	if env.rounds == nil {
		return nil, fmt.Errorf("consensus rounds observer not initialized")
	}
	env.rounds.mu.RLock()
	defer env.rounds.mu.RUnlock()

	hr, ok := env.rounds.byH[height]
	if !ok {
		return nil, fmt.Errorf("not found for height %d", height)
	}

	res := &ResultConsensusRounds{
		Height:      hr.Height,
		CommitRound: hr.CommitRound,
	}
	
	keys := make([]int32, 0, len(hr.Rounds))
	for k := range hr.Rounds {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	for _, r := range keys {
		info := hr.Rounds[r]
		item := struct {
			Round           int32  `json:"round"`
			Proposer        string `json:"proposer"`
			PrevotesCount   int    `json:"prevotes_count"`
			PrecommitsCount int    `json:"precommits_count"`
		}{
			Round:           r,
			Proposer:        info.Proposer,
			PrevotesCount:   len(info.Prevotes),
			PrecommitsCount: len(info.Precommits),
		}
		res.Rounds = append(res.Rounds, item)
	}
	return res, nil
}

type ResultConsensusRoundDetail struct {
	Height     int64       `json:"height"`
	Round      int32       `json:"round"`
	Proposer   string      `json:"proposer"`
	Prevotes   []RoundVote `json:"prevotes"`
	Precommits []RoundVote `json:"precommits"`
}

func (env *Environment) ConsensusRoundDetail(_ *rpctypes.Context, height int64, round int32) (*ResultConsensusRoundDetail, error) {
	if env.rounds == nil {
		return nil, fmt.Errorf("consensus rounds observer not initialized")
	}
	env.rounds.mu.RLock()
	defer env.rounds.mu.RUnlock()

	hr, ok := env.rounds.byH[height]
	if !ok {
		return nil, fmt.Errorf("not found for height %d", height)
	}
	ri, ok := hr.Rounds[round]
	if !ok {
		return nil, fmt.Errorf("not found for height %d round %d", height, round)
	}
	
	if ri.Prevotes == nil {
		ri.Prevotes = []RoundVote{}
	}
	if ri.Precommits == nil {
		ri.Precommits = []RoundVote{}
	}
	
	return &ResultConsensusRoundDetail{
		Height:     height,
		Round:      round,
		Proposer:   ri.Proposer,
		Prevotes:   ri.Prevotes,
		Precommits: ri.Precommits,
	}, nil
}