package pbench

import (
	"fmt"
	"path/filepath"

	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/actors/builtin/miner"
	"github.com/filecoin-project/lotus/extern/sector-storage/ffiwrapper"
	"github.com/filecoin-project/lotus/extern/sector-storage/ffiwrapper/basicfs"
	"github.com/filecoin-project/specs-storage/storage"
	"github.com/gwaylib/errors"
	"github.com/ipfs/go-cid"
	"golang.org/x/xerrors"
)

// copy from sector-storage/stores/filetype.go
func ParseSectorID(baseName string) (abi.SectorID, error) {
	var n abi.SectorNumber
	var mid abi.ActorID
	read, err := fmt.Sscanf(baseName, "s-f0%d-%d", &mid, &n)
	if err != nil {
		read, err = fmt.Sscanf(baseName, "s-t0%d-%d", &mid, &n)
		if err != nil {
			return abi.SectorID{}, xerrors.Errorf("sscanf sector name ('%s'): %w", baseName, err)
		}
	}

	if read != 2 {
		return abi.SectorID{}, xerrors.Errorf("parseSectorID expected to scan 2 values, got %d", read)
	}

	return abi.SectorID{
		Miner:  mid,
		Number: n,
	}, nil
}

func ParseMinerID(baseName string) (abi.ActorID, error) {
	var mid abi.ActorID
	read, err := fmt.Sscanf(baseName, "s-f0%d", &mid)
	if err != nil {
		read, err = fmt.Sscanf(baseName, "s-t0%d", &mid)
		if err != nil {
			return mid, xerrors.Errorf("sscanf sector name ('%s'): %w", baseName, err)
		}
	}

	if read != 1 {
		return mid, xerrors.Errorf("parseMinerID expected to scan 2 values, got %d", read)
	}

	return mid, nil
}

// copy from sector-storage/stores/filetype.go
func SectorName(sid abi.SectorID) string {
	return fmt.Sprintf("s-f0%d-%d", sid.Miner, sid.Number)
}

func MinerID(miner abi.ActorID) string {
	return fmt.Sprintf("s-f0%d", miner)
}

type SectorFile struct {
	SectorId string

	SealedRepo        string
	SealedStorageId   int64  // 0 not filled.
	SealedStorageType string // empty not filled.

	UnsealedRepo        string
	UnsealedStorageId   int64  // 0 not filled.
	UnsealedStorageType string // empty not filled.
	IsMarketSector      bool
}

func (f *SectorFile) HasRepo() bool {
	return len(f.SealedRepo) > 0 && len(f.UnsealedRepo) > 0
}

func (f *SectorFile) SectorID() abi.SectorID {
	id, err := ParseSectorID(f.SectorId)
	if err != nil {
		// should not reach here.
		panic(err)
	}
	return id
}

func (f *SectorFile) UnsealedFile() string {
	return filepath.Join(f.UnsealedRepo, "unsealed", f.SectorId)
}
func (f *SectorFile) UpdatePath() string {
	return filepath.Join(f.UnsealedRepo, "update", f.SectorId)
}
func (f *SectorFile) UpdateCachePath() string {
	return filepath.Join(f.UnsealedRepo, "update-cache", f.SectorId)
}
func (f *SectorFile) SealedFile() string {
	return filepath.Join(f.SealedRepo, "sealed", f.SectorId)
}
func (f *SectorFile) CachePath() string {
	return filepath.Join(f.SealedRepo, "cache", f.SectorId)
}

type SectorRef struct {
	storage.SectorRef
	SectorFile
}

type WorkerTask struct {
	Sector SectorRef

	// preCommit2
	PreCommit1Out storage.PreCommit1Out
}

type Sealer struct {
	*ffiwrapper.Sealer
	rootPath string
}

func NewSealer(rootPath string) (*Sealer, error) {
	sbfs := &basicfs.Provider{
		Root: rootPath,
	}
	s, err := ffiwrapper.New(sbfs)
	if err != nil {
		return nil, errors.As(err)
	}
	return &Sealer{
		Sealer:   s,
		rootPath: rootPath,
	}, nil
}

func (s *Sealer) RepoPath() string {
	return s.rootPath
}

type PreCommit1Out []byte

type Commit1Out []byte

type Proof []byte

type SectorCids struct {
	Unsealed cid.Cid
	Sealed   cid.Cid
}

func spt(ssize abi.SectorSize) abi.RegisteredSealProof {
	spt, err := miner.SealProofTypeFromSectorSize(ssize, build.NewestNetworkVersion)
	if err != nil {
		panic(err)
	}

	return spt
}
