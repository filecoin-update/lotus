package sectoraccessor

import (
	"context"
	"errors"
	"fmt"
	"github.com/gogf/gf/v2/net/gclient"
	"io"
	"os"

	logging "github.com/ipfs/go-log/v2"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/dagstore/mount"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-fil-markets/retrievalmarket"
	"github.com/filecoin-project/go-state-types/abi"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/api/v1api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/markets/dagstore"
	"github.com/filecoin-project/lotus/node/modules/dtypes"
	"github.com/filecoin-project/lotus/storage/sealer"
	"github.com/filecoin-project/lotus/storage/sectorblocks"

	"github.com/gogf/gf/v2/frame/g"
)

var log = logging.Logger("sectoraccessor")

type sectorAccessor struct {
	maddr address.Address
	secb  sectorblocks.SectorBuilder
	pp    sealer.PieceProvider
	full  v1api.FullNode
}

var _ retrievalmarket.SectorAccessor = (*sectorAccessor)(nil)

func NewSectorAccessor(maddr dtypes.MinerAddress, secb sectorblocks.SectorBuilder, pp sealer.PieceProvider, full v1api.FullNode) dagstore.SectorAccessor {
	return &sectorAccessor{address.Address(maddr), secb, pp, full}
}

func (sa *sectorAccessor) UnsealSector(ctx context.Context, sectorID abi.SectorNumber, pieceOffset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (io.ReadCloser, error) {
	return sa.UnsealSectorAt(ctx, sectorID, pieceOffset, length)
}

func (sa *sectorAccessor) UnsealSectorAt(ctx context.Context, sectorID abi.SectorNumber, pieceOffset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (mount.Reader, error) {
	log.Debugf("get sector %d, pieceOffset %d, length %d", sectorID, pieceOffset, length)

	si, err := sa.sectorsStatus(ctx, sectorID, false)
	if err != nil {
		return nil, err
	}

	piece := si.Pieces[0]
	if pieceOffset > 0 && len(si.Pieces) > 1 {
		piece = si.Pieces[1]
	}

	minioEndpoint, ok := os.LookupEnv("MINIO_ENDPOINT")
	if !ok {
		return nil, errors.New("place config env for minio endpoint")
	}
	url := fmt.Sprintf("%s/%s.car", minioEndpoint, piece.Piece.PieceCID.String())

	c := g.Client()
	//headerRange := fmt.Sprintf("bytes=0-%d", length)
	//c.SetHeader("Range", headerRange)
	if r, err := c.Get(ctx, url); err != nil {
		return nil, err
	} else {
		defer func(r *gclient.Response) {
			var err = r.Close()
			if err != nil {
				log.Debugf("http client close error: %s", err.Error())
			}
		}(r)
		if r.StatusCode == 404 {
			return nil, xerrors.New("not fond car")
		} else if r.StatusCode == 401 {
			return nil, xerrors.New("no permission")
		}
		data := mount.BytesMount{Bytes: r.ReadAll()}
		return data.Fetch(ctx)
	}

}

func (sa *sectorAccessor) IsUnsealed(ctx context.Context, sectorID abi.SectorNumber, offset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (bool, error) {
	return true, nil
}

func (sa *sectorAccessor) sectorsStatus(ctx context.Context, sid abi.SectorNumber, showOnChainInfo bool) (api.SectorInfo, error) {
	sInfo, err := sa.secb.SectorsStatus(ctx, sid, false)
	if err != nil {
		return api.SectorInfo{}, err
	}

	if !showOnChainInfo {
		return sInfo, nil
	}

	onChainInfo, err := sa.full.StateSectorGetInfo(ctx, sa.maddr, sid, types.EmptyTSK)
	if err != nil {
		return sInfo, err
	}
	if onChainInfo == nil {
		return sInfo, nil
	}
	sInfo.SealProof = onChainInfo.SealProof
	sInfo.Activation = onChainInfo.Activation
	sInfo.Expiration = onChainInfo.Expiration
	sInfo.DealWeight = onChainInfo.DealWeight
	sInfo.VerifiedDealWeight = onChainInfo.VerifiedDealWeight
	sInfo.InitialPledge = onChainInfo.InitialPledge

	ex, err := sa.full.StateSectorExpiration(ctx, sa.maddr, sid, types.EmptyTSK)
	if err != nil {
		return sInfo, nil
	}
	sInfo.OnTime = ex.OnTime
	sInfo.Early = ex.Early

	return sInfo, nil
}
