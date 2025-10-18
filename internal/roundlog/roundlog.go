// internal/roundlog/roundlog.go
package roundlog

import (
	"sync"
	"time"
)

type RoundRecord struct {
	Round      int32     `json:"round"`
	Proposer   string    `json:"proposer"`
	StartedAt  time.Time `json:"started_at"`
	Prevotes   int       `json:"prevotes_count,omitempty"`
	Precommits int       `json:"precommits_count,omitempty"`
}

type HeightRecord struct {
	Height      int64         `json:"height"`
	CommitRound int32         `json:"commit_round"`
	Rounds      []RoundRecord `json:"rounds"`
}

type Recorder struct {
	mu       sync.RWMutex
	cap      int
	order    []int64
	byHeight map[int64]*HeightRecord
}

func NewRecorder(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = 1024
	}
	return &Recorder{
		cap:      capacity,
		byHeight: make(map[int64]*HeightRecord),
		order:    make([]int64, 0, capacity),
	}
}

var defaultRec = NewRecorder(4096) // Son 4096 yükseklik tutulur.

func Default() *Recorder { return defaultRec }

func (r *Recorder) OnNewRound(height int64, round int32, proposer string, t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	hr := r.ensure(height)
	for _, rr := range hr.Rounds {
		if rr.Round == round {
			return // duplicate guard
		}
	}
	hr.Rounds = append(hr.Rounds, RoundRecord{
		Round:     round,
		Proposer:  proposer,
		StartedAt: t,
	})
}

func (r *Recorder) OnPrevoteCount(height int64, round int32, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if hr, ok := r.byHeight[height]; ok {
		for i := range hr.Rounds {
			if hr.Rounds[i].Round == round {
				hr.Rounds[i].Prevotes = n
				return
			}
		}
	}
}

func (r *Recorder) OnPrecommitCount(height int64, round int32, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if hr, ok := r.byHeight[height]; ok {
		for i := range hr.Rounds {
			if hr.Rounds[i].Round == round {
				hr.Rounds[i].Precommits = n
				return
			}
		}
	}
}

func (r *Recorder) SetCommitRound(height int64, round int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	hr := r.ensure(height)
	hr.CommitRound = round
}

func (r *Recorder) Get(height int64) *HeightRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if hr, ok := r.byHeight[height]; ok {
		out := *hr
		out.Rounds = append([]RoundRecord(nil), hr.Rounds...)
		return &out
	}
	return nil
}

func (r *Recorder) ensure(height int64) *HeightRecord {
	if hr, ok := r.byHeight[height]; ok {
		return hr
	}
	if len(r.order) >= r.cap {
		oldest := r.order[0]
		delete(r.byHeight, oldest)
		r.order = r.order[1:]
	}
	hr := &HeightRecord{Height: height}
	r.byHeight[height] = hr
	r.order = append(r.order, height)
	return hr
}
