// rpc/core/proposer.go
package core

import (
	"fmt"

	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	"github.com/cometbft/cometbft/types"
)

type RoundProposer struct {
	Round    int32            `json:"round"`
	Address  string           `json:"address"`
	Proposer *types.Validator `json:"proposer,omitempty"` // istersen sadece Address döndürebilirsin
}

type ResultProposerSchedule struct {
	Height int64           `json:"height"`
	Rounds []RoundProposer `json:"rounds"`
}

func (env *Environment) ProposerSchedule(
	_ *rpctypes.Context,
	heightPtr *int64,
	roundsPtr *int,
) (*ResultProposerSchedule, error) {

	var height int64
	if heightPtr != nil && *heightPtr > 0 {
		height = *heightPtr
	} else {
		// Varsayılan: bir sonraki blok
		height = env.ConsensusState.GetLastHeight() + 1
	}

	rounds := 10
	if roundsPtr != nil && *roundsPtr > 0 {
		if *roundsPtr > 1000 {
			rounds = 1000 // koruma
		} else {
			rounds = *roundsPtr
		}
	}

	// Bu yükseklikteki validator seti (priority’leri bu yükseklik için "ilerletilmiş" halde gelir)
	vals, err := env.StateStore.LoadValidators(height)
	if err != nil {
		return nil, fmt.Errorf("load validators at height %d: %w", height, err)
	}

	// Güvenli kopya (proto çevrimi ile derin kopya)
	vp, err := vals.ToProto()
	if err != nil {
		return nil, fmt.Errorf("to proto: %w", err)
	}
	valsCopy, err := types.ValidatorSetFromProto(vp)
	if err != nil {
		return nil, fmt.Errorf("from proto: %w", err)
	}

	out := make([]RoundProposer, 0, rounds)
	for r := 0; r < rounds; r++ {
		// Bir tur artır: en yüksek priority’li proposer seçilir ve priority’si totalVP kadar düşürülür.
		valsCopy.IncrementProposerPriority(1)
		p := valsCopy.Proposer
		out = append(out, RoundProposer{
			Round:    int32(r),
			Address:  p.Address.String(),
			Proposer: p,
		})
	}

	return &ResultProposerSchedule{
		Height: height,
		Rounds: out,
	}, nil
}
