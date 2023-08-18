package dagstore

import (
	"context"
	"errors"
	"fmt"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"os"

	"github.com/ipfs/go-cid"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/dagstore/mount"
	"github.com/filecoin-project/dagstore/throttle"
	"github.com/filecoin-project/go-fil-markets/piecestore"
	"github.com/filecoin-project/go-fil-markets/retrievalmarket"
	"github.com/filecoin-project/go-fil-markets/shared"
	"github.com/filecoin-project/go-state-types/abi"
)

//go:generate go run github.com/golang/mock/mockgen -destination=mocks/mock_lotus_accessor.go -package=mock_dagstore . MinerAPI

type MinerAPI interface {
	FetchUnsealedPiece(ctx context.Context, pieceCid cid.Cid) (mount.Reader, error)
	GetUnpaddedCARSize(ctx context.Context, pieceCid cid.Cid) (uint64, error)
	IsUnsealed(ctx context.Context, pieceCid cid.Cid) (bool, error)
	Start(ctx context.Context) error
}

type SectorAccessor interface {
	retrievalmarket.SectorAccessor

	UnsealSectorAt(ctx context.Context, sectorID abi.SectorNumber, pieceOffset abi.UnpaddedPieceSize, length abi.UnpaddedPieceSize) (mount.Reader, error)
}

type minerAPI struct {
	pieceStore     piecestore.PieceStore
	sa             SectorAccessor
	throttle       throttle.Throttler
	unsealThrottle throttle.Throttler
	readyMgr       *shared.ReadyManager
}

var _ MinerAPI = (*minerAPI)(nil)

func NewMinerAPI(store piecestore.PieceStore, sa SectorAccessor, concurrency int, unsealConcurrency int) MinerAPI {
	var unsealThrottle throttle.Throttler
	if unsealConcurrency == 0 {
		unsealThrottle = throttle.Noop()
	} else {
		unsealThrottle = throttle.Fixed(unsealConcurrency)
	}
	return &minerAPI{
		pieceStore:     store,
		sa:             sa,
		throttle:       throttle.Fixed(concurrency),
		unsealThrottle: unsealThrottle,
		readyMgr:       shared.NewReadyManager(),
	}
}

func (m *minerAPI) Start(_ context.Context) error {
	return m.readyMgr.FireReady(nil)
}

func (m *minerAPI) IsUnsealed(ctx context.Context, pieceCid cid.Cid) (bool, error) {
	return true, nil
}

func (m *minerAPI) FetchUnsealedPiece(ctx context.Context, pieceCid cid.Cid) (mount.Reader, error) {
	minioEndpoint, ok := os.LookupEnv("MINIO_ENDPOINT")
	if !ok {
		return nil, errors.New("place config env for minio endpoint")
	}
	url := fmt.Sprintf("%s/%s.car", minioEndpoint, pieceCid.String())

	c := g.Client()
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

func (m *minerAPI) GetUnpaddedCARSize(ctx context.Context, pieceCid cid.Cid) (uint64, error) {
	err := m.readyMgr.AwaitReady()
	if err != nil {
		return 0, err
	}

	pieceInfo, err := m.pieceStore.GetPieceInfo(pieceCid)
	if err != nil {
		return 0, xerrors.Errorf("failed to fetch pieceInfo for piece %s: %w", pieceCid, err)
	}

	if len(pieceInfo.Deals) == 0 {
		return 0, xerrors.Errorf("no storage deals found for piece %s", pieceCid)
	}

	len := pieceInfo.Deals[0].Length

	return uint64(len), nil
}
