package core

import (
	"context"

	ethpb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
)

// Service represents a service that handles the internal
// logic of managing the full PoS beacon chain.
type Service struct {
	ctx      context.Context
	cancel   context.CancelFunc
	snapshot *ethpb.LightClientSnapShot
	updates  []*ethpb.LightClientUpdate
}
