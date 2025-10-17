// rpc/core/rounds.go
package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	cmtquery "github.com/cometbft/cometbft/libs/pubsub/query"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	"github.com/cometbft/cometbft/types"
)

// -------------------- DATA MODEL --------------------

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
	order []int64                 // tutulan yüksekliklerin sırası
	byH   map[int64]*HeightRounds // height -> snapshot
	cap   int                     // max height sayısı
}

func (c *roundsCache) getOrCreate(H int64, R int32) *RoundInfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	hr, ok := c.byH[H]
	if !ok {
		// yeni height için kayıt oluştur ve ringe ekle
		hr = &HeightRounds{Height: H, Rounds: make(map[int32]*RoundInfo)}
		c.byH[H] = hr
		c.order = append(c.order, H)
		c.evictIfNeeded()
	}

	ri, ok := hr.Rounds[R]
	if !ok {
		ri = &RoundInfo{Round: R}
		hr.Rounds[R] = ri
	}
	return ri
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
	// proposer bilgisi EventDataNewRound.Proposer.Address içinde
	ri.Proposer = ev.Proposer.Address.String()
}

func (c *roundsCache) onVote(ev types.EventDataVote) {
	if ev.Vote == nil {
		return
	}
	rt := "prevote"
	if ev.Vote.Type == types.PrecommitType {
		rt = "precommit"
	}
	ri := c.getOrCreate(ev.Vote.Height, ev.Vote.Round)
	r := RoundVote{
		Validator: ev.Vote.ValidatorAddress.String(),
		Type:      rt,
		Timestamp: ev.Vote.Timestamp, // NOTE: bazı ağlarda boş gelebilir
	}
	if rt == "prevote" {
		ri.Prevotes = append(ri.Prevotes, r)
	} else {
		ri.Precommits = append(ri.Precommits, r)
	}
}

func (c *roundsCache) onCompleteProposal(ev types.EventDataCompleteProposal) {
	// commit'e giden round genellikle budur; final teyidi /block ile yapabilirsin
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
}

// -------------------- OBSERVER INIT --------------------

// Environment struct'ı env.go'da; rounds alanını orada ekleyeceksin.
// Bu metod çağrıldığında EventBus'tan konsensus eventlerini dinler.
func (env *Environment) InitConsensusRoundsObserver(size int) error {
	if size <= 0 {
		size = 10000
	}
	env.rounds = &roundsCache{
		byH: make(map[int64]*HeightRounds),
		cap: size,
	}

	// Üç ayrı subscription (NewRound, Vote, CompleteProposal)
	// Tek OR query ile de yapılabilir ama ayrı ayrı daha temiz.
	// 1) NewRound
	subNR, err := env.EventBus.Subscribe(context.Background(), "rounds-observer-newround", types.EventQueryNewRound)
	if err != nil {
		return err
	}
	// 2) Vote
	subV, err := env.EventBus.Subscribe(context.Background(), "rounds-observer-vote", types.EventQueryVote)
	if err != nil {
		return err
	}
	// 3) CompleteProposal
	qCP, _ := cmtquery.New(fmt.Sprintf("%s='%s'", types.EventTypeKey, types.EventCompleteProposal))
	subCP, err := env.EventBus.Subscribe(context.Background(), "rounds-observer-completeproposal", qCP)
	if err != nil {
		return err
	}

	// consumer goroutine'leri
	go func() {
		for msg := range subNR.Out() {
			if ev, ok := msg.Data.(types.EventDataNewRound); ok {
				env.rounds.onNewRound(ev)
			}
		}
	}()
	go func() {
		for msg := range subV.Out() {
			if ev, ok := msg.Data.(types.EventDataVote); ok {
				env.rounds.onVote(ev)
			}
		}
	}()
	go func() {
		for msg := range subCP.Out() {
			if ev, ok := msg.Data.(types.EventDataCompleteProposal); ok {
				env.rounds.onCompleteProposal(ev)
			}
		}
	}()

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

// height -> round özetleri
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
	for r, info := range hr.Rounds {
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
	Height    int64       `json:"height"`
	Round     int32       `json:"round"`
	Proposer  string      `json:"proposer"`
	Prevotes  []RoundVote `json:"prevotes"`
	Precommits []RoundVote `json:"precommits"`
}

// height+round -> detay
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
	return &ResultConsensusRoundDetail{
		Height:     height,
		Round:      round,
		Proposer:   ri.Proposer,
		Prevotes:   ri.Prevotes,
		Precommits: ri.Precommits,
	}, nil
}
