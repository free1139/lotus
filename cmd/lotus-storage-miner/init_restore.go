package main

import (
	"os"

	"github.com/docker/go-units"
	"github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-address"
	paramfetch "github.com/filecoin-project/go-paramfetch"
	"github.com/filecoin-project/go-state-types/big"

	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/filecoin-project/lotus/lib/backupds"
	"github.com/filecoin-project/lotus/node/repo"
)

var initRestoreCmd = &cli.Command{
	Name:  "restore",
	Usage: "Initialize a lotus miner repo from a backup",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "nosync",
			Usage: "don't check full-node sync status",
		},
		// TODO: Config
		// TODO: Storage paths
	},
	ArgsUsage: "[backupFile]",
	Action: func(cctx *cli.Context) error {
		log.Info("Initializing lotus miner using a backup")
		if cctx.Args().Len() != 1 {
			return xerrors.Errorf("expected 1 argument")
		}

		log.Info("Trying to connect to full node RPC")

		api, closer, err := lcli.GetFullNodeAPI(cctx) // TODO: consider storing full node address in config
		if err != nil {
			return err
		}
		defer closer()

		log.Info("Checking full node version")

		ctx := lcli.ReqContext(cctx)

		v, err := api.Version(ctx)
		if err != nil {
			return err
		}

		if !v.APIVersion.EqMajorMinor(build.FullAPIVersion) {
			return xerrors.Errorf("Remote API version didn't match (expected %s, remote %s)", build.FullAPIVersion, v.APIVersion)
		}

		if !cctx.Bool("nosync") {
			if err := lcli.SyncWait(ctx, api); err != nil {
				return xerrors.Errorf("sync wait: %w", err)
			}
		}

		f, err := os.Open(cctx.Args().First())
		if err != nil {
			return xerrors.Errorf("opening backup file: %w", err)
		}
		defer f.Close() // nolint:errcheck

		log.Info("Checking if repo exists")

		repoPath := cctx.String(FlagMinerRepo)
		r, err := repo.NewFS(repoPath)
		if err != nil {
			return err
		}

		ok, err := r.Exists()
		if err != nil {
			return err
		}
		if ok {
			return xerrors.Errorf("repo at '%s' is already initialized", cctx.String(FlagMinerRepo))
		}

		log.Info("Initializing repo")

		if err := r.Init(repo.StorageMiner); err != nil {
			return err
		}

		lr, err := r.Lock(repo.StorageMiner)
		if err != nil {
			return err
		}
		defer lr.Close() //nolint:errcheck

		log.Info("Restoring metadata backup")

		mds, err := lr.Datastore("/metadata")
		if err != nil {
			return err
		}

		if err := backupds.RestoreInto(f, mds); err != nil {
			return xerrors.Errorf("restoring metadata: %w", err)
		}

		log.Info("Checking actor metadata")

		abytes, err := mds.Get(datastore.NewKey("miner-address"))
		if err != nil {
			return xerrors.Errorf("getting actor address from metadata datastore: %w", err)
		}

		maddr, err := address.NewFromBytes(abytes)
		if err != nil {
			return xerrors.Errorf("parsing actor address: %w", err)
		}

		log.Info("ACTOR ADDRESS: ", maddr.String())

		mi, err := api.StateMinerInfo(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return xerrors.Errorf("getting miner info: %w", err)
		}

		log.Info("SECTOR SIZE: ", units.BytesSize(float64(mi.SectorSize)))

		has, err := api.WalletHas(ctx, mi.Worker)
		if err != nil {
			return xerrors.Errorf("checking worker address: %w", err)
		}

		if !has {
			return xerrors.Errorf("worker address %s for miner actor %s not present in full node wallet", mi.Worker, maddr)
		}

		log.Info("Checking proof parameters")

		if err := paramfetch.GetParams(ctx, build.ParametersJSON(), uint64(mi.SectorSize)); err != nil {
			return xerrors.Errorf("fetching proof parameters: %w", err)
		}

		log.Info("Initializing libp2p identity")

		p2pSk, err := makeHostKey(lr)
		if err != nil {
			return xerrors.Errorf("make host key: %w", err)
		}

		peerid, err := peer.IDFromPrivateKey(p2pSk)
		if err != nil {
			return xerrors.Errorf("peer ID from private key: %w", err)
		}

		log.Info("Configuring miner actor")

		if err := configureStorageMiner(ctx, api, maddr, peerid, big.Zero()); err != nil {
			return err
		}

		return nil
	},
}
