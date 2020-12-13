package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/gogo/protobuf/proto"
	pb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	ethpb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/bls"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/wealdtech/go-bytesutil"
)

func (s *Service) verifyUpdate(ctx context.Context, update *ethpb.LightClientUpdate) error {
	if s.snapshot.Header.Slot < update.Header.Slot {
		return fmt.Errorf("update slot %d less than snap shot slot %d", update.Header.Slot, s.snapshot.Header.Slot)
	}

	snapShotPeriod := s.snapshot.Header.Slot / params.BeaconConfig().EpochsPerSyncCommitteePeriod
	updatePeriod := update.Header.Slot / params.BeaconConfig().EpochsPerSyncCommitteePeriod
	if updatePeriod != snapShotPeriod || updatePeriod != snapShotPeriod+1 {
		return fmt.Errorf("update period %d is not eligible with snap shot period %d", updatePeriod, snapShotPeriod)
	}

	signedHeader := &pb.BeaconBlockHeader{}
	if proto.Equal(update.FinalityHeader, &pb.BeaconBlockHeader{}) {
		signedHeader = update.Header
		for _, branch := range update.FinalityBranch {
			if bytesutil.ToBytes32(branch) != params.BeaconConfig().ZeroHash {
				return errors.New("finality branch is not zero hash")
			}
		}
		signedHeader = update.FinalityHeader
		// TODO: Verify valid merkle branch
	}

	syncCommittee := &ethpb.SyncCommittee{}
	if updatePeriod == snapShotPeriod {
		syncCommittee = s.snapshot.CurrentSyncCommittee
		for _, branch := range update.NextSyncCommitteeBranch {
			if bytesutil.ToBytes32(branch) != params.BeaconConfig().ZeroHash {
				return errors.New("next sync committee branch is not zero hash")
			}
		}
	} else {
		syncCommittee = s.snapshot.NextSyncCommittee
		// TODO: Verify valid merkle branch
	}

	if params.BeaconConfig().MinSyncCommitteeParticipants > uint64(len(update.SyncCommitteeBits.BitIndices())) {
		return fmt.Errorf("sync committee bit count %d less than config %d", update.SyncCommitteeBits.Count(), params.BeaconConfig().MinSyncCommitteeParticipants)
	}

	// TODO: Verify signature
	var pubkeys []bls.PublicKey
	for i, pk := range syncCommittee.Pubkeys {
		if update.SyncCommitteeBits.BitAt(uint64(i)) {
			pk, err := bls.PublicKeyFromBytes(pk)
			if err != nil {
				return err
			}
			pubkeys = append(pubkeys, pk)
		}
	}

	domain, err := helpers.ComputeDomain(params.BeaconConfig().DomainSyncCommittee, update.Fork.CurrentVersion, []byte{})
	if err != nil {
		return err
	}
	sig, err := bls.SignatureFromBytes(update.SyncCommitteeSignature)
	if err != nil {
		return err
	}
	sr, err := helpers.ComputeSigningRoot(signedHeader, domain)
	if err != nil {
		return err
	}
	if !sig.FastAggregateVerify(pubkeys, sr) {
		return errors.New("could not verify sync committee signature")
	}
	return nil
}

func (s *Service) applyUpdate(ctx context.Context, update *ethpb.LightClientUpdate) {
	snapShotPeriod := s.snapshot.Header.Slot / params.BeaconConfig().EpochsPerSyncCommitteePeriod
	updatePeriod := update.Header.Slot / params.BeaconConfig().EpochsPerSyncCommitteePeriod
	if updatePeriod == snapShotPeriod+1 {
		s.snapshot.CurrentSyncCommittee = s.snapshot.NextSyncCommittee
		s.snapshot.NextSyncCommittee = update.NextSyncCommittee
	}
	s.snapshot.Header = update.Header
}

func (s *Service) processUpdate(ctx context.Context, update *ethpb.LightClientUpdate, currentSlot uint64) error {
	if err := s.verifyUpdate(ctx, update); err != nil {
		return err
	}
	s.updates = append(s.updates, update)

	voted := uint64(len(update.SyncCommitteeBits.BitIndices()))
	total := update.SyncCommitteeBits.Count()
	if voted*3 > total*2 && !proto.Equal(update.Header, update.FinalityHeader) {
		// Update update when 2/3 quorum is reached with finality proof.
		s.applyUpdate(ctx, update)
		s.updates = []*ethpb.LightClientUpdate{}
	} else if currentSlot > s.snapshot.Header.Slot+params.BeaconConfig().LightClientUpdateTimeOut {
		bestUpdate := s.updates[0]
		bestCount := len(s.updates[0].SyncCommitteeBits.BitIndices())
		for _, clientUpdate := range s.updates[1:] {
			count := len(clientUpdate.SyncCommitteeBits.BitIndices())
			if count > bestCount {
				bestUpdate = clientUpdate
				bestCount = count
			}
		}
		s.applyUpdate(ctx, bestUpdate)
		s.updates = []*ethpb.LightClientUpdate{}
	}

	return nil
}
