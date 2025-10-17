// rpc/core/rounds.go
package core

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
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
	CommitRound int32                `json:"commit_round"` // CompleteProposal'dan doldurulur
	Rounds      map[int32]*RoundInfo `json:"rounds"`
}

type roundsCache struct {
	mu    sync.RWMutex
	order []int64                 // tutulan yüksekliklerin sırası
	byH   map[int64]*HeightRounds // height -> snapshot
	cap   int                     // max height sayısı (ring kapasitesi)
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
	// v0.39.4: Proposer struct; adres boş olabilir → uzunlukla kontrol et.
	addr := ev.Proposer.Address
	if len(addr) > 0 {
		ri.Proposer = addr.String()
	} else {
		ri.Proposer = ""
	}
}

func (c *roundsCache) onVote(ev types.EventDataVote) {
	if ev.Vote == nil {
		return
	}
	rt := "prevote"
	if ev.Vote.Type == cmtproto.PrecommitType { // DİKKAT: cmtproto
		rt = "precommit"
	}
	ri := c.getOrCreate(ev.Vote.Height, ev.Vote.Round)
	r := RoundVote{
		Validator: ev.Vote.ValidatorAddress.String(),
		Type:      rt,
		Timestamp: ev.Vote.Timestamp, // bazı ağlarda boş olabilir
	}
	if rt == "prevote" {
		ri.Prevotes = append(ri.Prevotes, r)
	} else {
		ri.Precommits = append(ri.Precommits, r)
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
}

// -------------------- OBSERVER INIT --------------------

func (env *Environment) InitConsensusRoundsObserver(size int) error {
	if size <= 0 {
		size = 10000
	}
	env.rounds = &roundsCache{
		byH: make(map[int64]*HeightRounds),
		cap: size,
	}

	ctx := context.Background()

	// Query'leri sabit isimlerle kur (sürüm farklarına dayanıklı)
	subNR, err := env.EventBus.Subscribe(ctx, "rounds-newround",
		types.QueryForEvent(types.EventNewRound))
	if err != nil {
		return err
	}

	subV, err := env.EventBus.Subscribe(ctx, "rounds-vote",
		types.QueryForEvent(types.EventVote))
	if err != nil {
		return err
	}

	subCP, err := env.EventBus.Subscribe(ctx, "rounds-completeproposal",
		types.QueryForEvent(types.EventCompleteProposal))
	if err != nil {
		return err
	}

	// Round ilerlemelerini daha sağlam yakalamak için adım event’leri:
	subStep, err := env.EventBus.Subscribe(ctx, "rounds-step",
		types.QueryForEvent(types.EventNewRoundStep))
	if err != nil {
		return err
	}

	// Consumer goroutineleri
	go func() {
		for msg := range subNR.Out() {
			if ev, ok := msg.Data().(types.EventDataNewRound); ok { // DİKKAT: Data()
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
	go func() {
		for msg := range subStep.Out() {
			if ev, ok := msg.Data().(types.EventDataRoundState); ok {
				// round>0 oluştuğunda da kayıt açalım
				_ = env.rounds.getOrCreate(ev.Height, ev.Round)
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

	// round anahtarlarını sırala (stabil çıktı)
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

// round paramını int32 yerine int al → HTTP-RPC unmarshal hatası kesilir
func (env *Environment) ConsensusRoundDetail(_ *rpctypes.Context, height int64, round int) (*ResultConsensusRoundDetail, error) {
	if env.rounds == nil {
		return nil, fmt.Errorf("consensus rounds observer not initialized")
	}
	env.rounds.mu.RLock()
	defer env.rounds.mu.RUnlock()

	hr, ok := env.rounds.byH[height]
	if !ok {
		return nil, fmt.Errorf("not found for height %d", height)
	}
	ri, ok := hr.Rounds[int32(round)]
	if !ok {
		return nil, fmt.Errorf("not found for height %d round %d", height, round)
	}

	// nil slice yerine boş dizi döndür (JSON 'null' olmasın)
	if ri.Prevotes == nil {
		ri.Prevotes = []RoundVote{}
	}
	if ri.Precommits == nil {
		ri.Precommits = []RoundVote{}
	}

	return &ResultConsensusRoundDetail{
		Height:     height,
		Round:      int32(round),
		Proposer:   ri.Proposer,
		Prevotes:   ri.Prevotes,
		Precommits: ri.Precommits,
	}, nil
}
