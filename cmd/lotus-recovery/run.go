package main

import (
	"errors"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	lcli "github.com/filecoin-project/lotus/cli"
	cliutil "github.com/filecoin-project/lotus/cli/util"
	"github.com/filecoin-project/lotus/storage/sealer/ffiwrapper"
	"github.com/filecoin-project/lotus/storage/sealer/ffiwrapper/basicfs"
	"github.com/filecoin-project/lotus/storage/sealer/storiface"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/mitchellh/go-homedir"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"
	"os"
)

var runCmd = &cli.Command{
	Name:  "run",
	Usage: "running lotus worker",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "path",
			Usage: "path",
			Value: "/seal/recovery",
		},
		&cli.StringFlag{
			Name:  "url",
			Usage: "url",
			Value: "http://minio.com:9000/car/",
		},
		&cli.Uint64SliceFlag{
			Name:  "sis",
			Usage: "sis",
		},
	},
	Action: func(cctx *cli.Context) error {

		ctx := lcli.ReqContext(cctx)

		sis := cctx.Uint64Slice("sis")
		if len(sis) < 1 {
			return xerrors.New("扇区号不存在")
		}

		nodeApi, closer, err := lcli.GetStorageMinerAPI(cctx, cliutil.StorageMinerUseHttp)
		if err != nil {
			return errors.New("初始化节点API错误")
		}
		defer closer()

		sid := abi.SectorNumber(sis[0])

		sectorInfo, err := nodeApi.SectorsStatus(ctx, sid, false)

		sealingPath, err := homedir.Expand(cctx.String("path"))
		if err != nil {
			log.Errorf("Sector (%s) ,expands the path error: %v", sid.String(), err)
		}

		tempDir, err := os.MkdirTemp(sealingPath, fmt.Sprintf("recover-%s", sid.String()))

		sb, err := ffiwrapper.New(&basicfs.Provider{
			Root: tempDir,
		})

		url := cctx.String("url")

		pi, err := nodeApi.PiecesGetPieceInfo(ctx, sectorInfo.Pieces[0].Piece.PieceCID)
		url = fmt.Sprintf("%s/%s.car", url, pi.PieceCID.String())
		existingPieceSizes := make([]abi.UnpaddedPieceSize, 0)
		pieceSize := abi.PaddedPieceSize(34359738368)

		rsp, err := g.Client().Get(ctx, url)
		defer func(rsp *gclient.Response) {
			err := rsp.Close()
			if err != nil {
				log.Error(err)
			}
		}(rsp)

		addr, err := nodeApi.ActorAddress(ctx)
		actorID, err := address.IDFromAddress(addr)

		sector := storiface.SectorRef{
			ID: abi.SectorID{
				Miner:  abi.ActorID(actorID),
				Number: sectorInfo.SectorID,
			},
			ProofType: sectorInfo.SealProof,
		}
		npi, err := sb.AddPiece(cctx.Context, sector, existingPieceSizes, pieceSize.Unpadded(), rsp.Body)

		log.Info(pi.PieceCID.String())
		log.Info(npi.PieceCID.String())

		return nil
	},
}
