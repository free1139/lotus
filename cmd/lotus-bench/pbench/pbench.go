package pbench

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/go-units"
	"github.com/filecoin-project/go-state-types/abi"
	lapi "github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/actors/policy"
	"github.com/filecoin-project/specs-storage/storage"
	"github.com/google/uuid"
	"github.com/gwaylib/errors"
	logging "github.com/ipfs/go-log/v2"
	"github.com/minio/blake2b-simd"
	"github.com/mitchellh/go-homedir"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"
)

var log = logging.Logger("p-bench")

var ParallelBenchCmd = &cli.Command{
	Name:  "p-bench",
	Usage: "Benchmark for parallel seal",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "storage-dir",
			Value: "~/.lotus-bench",
			Usage: "path to the storage directory that will store sectors long term",
		},
		&cli.StringFlag{
			Name:  "sector-size",
			Value: "512MiB",
			Usage: "size of the sectors in bytes, i.e. 32GiB",
		},
		&cli.BoolFlag{
			Name:  "no-gpu",
			Usage: "disable gpu usage for the benchmark run",
		},
		&cli.BoolFlag{
			Name:  "order",
			Usage: "run the tasks in order",
			Value: true,
		},
		&cli.BoolFlag{
			Name:  "auto-release",
			Usage: "release the disk data when task end",
			Value: false,
		},
		&cli.IntFlag{
			Name:  "task-pool",
			Value: 1,
			Usage: "task producer. number of tasks want to testing",
		},
		&cli.IntFlag{
			Name:  "parallel-addpiece",
			Value: 1,
			Usage: "concurrency of addpiece. be limited with cpu, disk io. ",
		},
		&cli.IntFlag{
			Name:  "parallel-precommit1",
			Value: 1,
			Usage: "concurrency of precommit1. be limited with cpu, disk io. ",
		},
		&cli.IntFlag{
			Name:  "parallel-precommit2",
			Value: 1,
			Usage: "concurrency of precommit2. be limited with cpu, gpu, disk io. ",
		},
		&cli.IntFlag{
			Name:  "parallel-commit1",
			Value: 1,
		},
		&cli.IntFlag{
			Name:  "parallel-commit2",
			Value: 1,
			Usage: "concurrency of commit2. be limited with  gpu. ",
		},
	},
	Action: func(c *cli.Context) error {
		policy.AddSupportedProofTypes(abi.RegisteredSealProof_StackedDrg2KiBV1)

		return doBench(c)
	},
}

type ParallelBenchTask struct {
	Type int

	TicketPreimage []byte

	SectorSize abi.SectorSize
	SectorID   abi.SectorID

	Repo string

	// preCommit1
	Pieces []abi.PieceInfo // commit1 is need too.

	// preCommit2
	PreCommit1Out storage.PreCommit1Out

	// commit1
	Cids storage.SectorCids

	// commit2
	Commit1Out storage.Commit1Out

	// result
	Proof storage.Proof
}

func (t *ParallelBenchTask) SectorName() string {
	return SectorName(t.SectorID)
}

type ParallelBenchResult struct {
	sectorName string
	startTime  time.Time
	endTime    time.Time
}

func (p *ParallelBenchResult) String() string {
	return fmt.Sprintf("%s used:%s,start:%s,end:%s", p.sectorName, p.endTime.Sub(p.startTime), p.startTime.Format(time.RFC3339Nano), p.endTime.Format(time.RFC3339Nano))
}

type ParallelBenchResultSort []ParallelBenchResult

func (ps ParallelBenchResultSort) Len() int {
	return len(ps)
}
func (ps ParallelBenchResultSort) Swap(i, j int) {
	ps[i], ps[j] = ps[j], ps[i]
}
func (ps ParallelBenchResultSort) Less(i, j int) bool {
	return ps[i].startTime.Sub(ps[j].startTime) < 0
}

var (
	parallelLock = sync.Mutex{}
	resultLk     = sync.Mutex{}

	apParallel   int32
	apLimit      int32
	apTaskChan   chan *ParallelBenchTask
	apWorkerChan chan bool
	apResult     = []ParallelBenchResult{}

	p1Parallel   int32
	p1Limit      int32
	p1TaskChan   chan *ParallelBenchTask
	p1WorkerChan chan bool
	p1Result     = []ParallelBenchResult{}

	p2Parallel   int32
	p2Limit      int32
	p2TaskChan   chan *ParallelBenchTask
	p2WorkerChan chan bool
	p2Result     = []ParallelBenchResult{}

	c1Parallel   int32
	c1Limit      int32
	c1TaskChan   chan *ParallelBenchTask
	c1WorkerChan chan bool
	c1Result     = []ParallelBenchResult{}

	c2Parallel   int32
	c2Limit      int32
	c2TaskChan   chan *ParallelBenchTask
	c2WorkerChan chan bool
	c2Result     = []ParallelBenchResult{}

	endEvent chan *ParallelBenchTask
)

func Statistics(result ParallelBenchResultSort) (sum, min, max time.Duration) {
	resultLk.Lock()
	defer resultLk.Unlock()

	sort.Sort(result)

	min = time.Duration(math.MaxInt64)
	for _, r := range result {
		used := r.endTime.Sub(r.startTime)
		if min > used {
			min = used
		}
		if max < used {
			max = used
		}
		sum += used
		fmt.Printf("%s\n", r.String())
	}
	return
}

const (
	TASK_KIND_ADDPIECE   = 0
	TASK_KIND_PRECOMMIT1 = 10
	TASK_KIND_PRECOMMIT2 = 20
	TASK_KIND_COMMIT1    = 30
	TASK_KIND_COMMIT2    = 40
)

func canParallel(kind int, order bool) bool {
	apRunning := atomic.LoadInt32(&apParallel) > 0
	p1Running := atomic.LoadInt32(&p1Parallel) > 0
	p2Running := atomic.LoadInt32(&p2Parallel) > 0
	c1Running := atomic.LoadInt32(&c1Parallel) > 0
	c2Running := atomic.LoadInt32(&c2Parallel) > 0
	switch kind {
	case TASK_KIND_ADDPIECE:
		if order {
			return atomic.LoadInt32(&apParallel) < apLimit && !p1Running && !p2Running && !c1Running && !c2Running
		}
		return atomic.LoadInt32(&apParallel) < apLimit
	case TASK_KIND_PRECOMMIT1:
		if order {
			return atomic.LoadInt32(&p1Parallel) < p1Limit && !apRunning && !p2Running && !c1Running && !c2Running
		}
		return atomic.LoadInt32(&p1Parallel) < p1Limit
	case TASK_KIND_PRECOMMIT2:
		if order {
			return atomic.LoadInt32(&p2Parallel) < p2Limit && !apRunning && !p1Running && !c1Running && !c2Running
		}
		return atomic.LoadInt32(&p2Parallel) < p2Limit
	case TASK_KIND_COMMIT1:
		if order {
			return atomic.LoadInt32(&c1Parallel) < c1Limit && !apRunning && !p1Running && !p2Running && !c2Running
		}
		return atomic.LoadInt32(&c1Parallel) < c1Limit
	case TASK_KIND_COMMIT2:
		if order {
			return atomic.LoadInt32(&c2Parallel) < c2Limit && !apRunning && !p1Running && !p2Running && !c1Running
		}
		return atomic.LoadInt32(&c2Parallel) < c2Limit
	}
	panic("not reach here")
}

func offsetParallelNum(kind int, offset int32) {
	switch kind {
	case TASK_KIND_ADDPIECE:
		atomic.AddInt32(&apParallel, offset)
	case TASK_KIND_PRECOMMIT1:
		atomic.AddInt32(&p1Parallel, offset)
	case TASK_KIND_PRECOMMIT2:
		atomic.AddInt32(&p2Parallel, offset)
	case TASK_KIND_COMMIT1:
		atomic.AddInt32(&c1Parallel, offset)
	case TASK_KIND_COMMIT2:
		atomic.AddInt32(&c2Parallel, offset)
	default:
		panic("not reach here")
	}
}

func doBench(c *cli.Context) error {
	if c.Bool("no-gpu") {
		err := os.Setenv("BELLMAN_NO_GPU", "1")
		if err != nil {
			return xerrors.Errorf("setting no-gpu flag: %w", err)
		}
	}
	// sector size
	sectorSizeInt, err := units.RAMInBytes(c.String("sector-size"))
	if err != nil {
		return err
	}
	sectorSize := abi.SectorSize(sectorSizeInt)
	orderRun := c.Bool("order")
	autoRelease := c.Bool("auto-release")
	taskPool := c.Int("task-pool")
	apLimit = int32(c.Int("parallel-addpiece"))
	p1Limit = int32(c.Int("parallel-precommit1"))
	p2Limit = int32(c.Int("parallel-precommit2"))
	c1Limit = int32(c.Int("parallel-commit1"))
	c2Limit = int32(c.Int("parallel-commit2"))
	apTaskChan = make(chan *ParallelBenchTask, apLimit)
	p1TaskChan = make(chan *ParallelBenchTask, p1Limit)
	p2TaskChan = make(chan *ParallelBenchTask, p2Limit)
	c1TaskChan = make(chan *ParallelBenchTask, c1Limit)
	c2TaskChan = make(chan *ParallelBenchTask, c2Limit)
	endEvent = make(chan *ParallelBenchTask, taskPool)

	apWorkerChan = make(chan bool, apLimit)
	p1WorkerChan = make(chan bool, p1Limit)
	p2WorkerChan = make(chan bool, p2Limit)
	c1WorkerChan = make(chan bool, c1Limit)
	c2WorkerChan = make(chan bool, c2Limit)
	//make all woker is idle
	for i := int32(0); i < apLimit; i++ {
		apWorkerChan <- true
	}
	for i := int32(0); i < p1Limit; i++ {
		p1WorkerChan <- true
	}
	for i := int32(0); i < p2Limit; i++ {
		p2WorkerChan <- true
	}
	for i := int32(0); i < c1Limit; i++ {
		c1WorkerChan <- true
	}
	for i := int32(0); i < c2Limit; i++ {
		c2WorkerChan <- true
	}

	// build repo
	sdir, err := homedir.Expand(c.String("storage-dir"))
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(sdir); err != nil {
			log.Warn("remove all: ", err)
		}
	}()
	err = os.MkdirAll(sdir, 0775) //nolint:gosec
	if err != nil {
		return xerrors.Errorf("creating sectorbuilder dir: %w", err)
	}
	sb, err := NewSealer(sdir)
	if err != nil {
		return err
	}

	// event producer
	go func() {
		for i := 0; i < taskPool; i++ {
			task := &ParallelBenchTask{
				Type:       TASK_KIND_ADDPIECE,
				SectorSize: sectorSize,
				SectorID: abi.SectorID{
					1000,
					abi.SectorNumber(i),
				},

				Repo: sdir,

				TicketPreimage: []byte(uuid.New().String()),
			}

			apTaskChan <- task
		}
	}()

	fmt.Println("[ctrl+c to exit]")
	exit := make(chan os.Signal, 2)
	signal.Notify(exit, os.Interrupt, os.Kill)
	ctx, cancel := context.WithCancel(context.TODO())
	work := func() {
		for {
			select {
			case <-apWorkerChan:
				prepareTask(ctx, sb, apWorkerChan, apTaskChan, TASK_KIND_ADDPIECE, orderRun)
			case <-p1WorkerChan:
				prepareTask(ctx, sb, p1WorkerChan, p1TaskChan, TASK_KIND_PRECOMMIT1, orderRun)
			case <-p2WorkerChan:
				prepareTask(ctx, sb, p2WorkerChan, p2TaskChan, TASK_KIND_PRECOMMIT2, orderRun)
			case <-c1WorkerChan:
				prepareTask(ctx, sb, c1WorkerChan, c1TaskChan, TASK_KIND_COMMIT1, orderRun)
			case <-c2WorkerChan:
				prepareTask(ctx, sb, c2WorkerChan, c2TaskChan, TASK_KIND_COMMIT2, orderRun)
			case <-ctx.Done():
				// exit
				fmt.Println("user canceled")
				return
			}
		}
	}

	for wNum := apLimit + p1Limit + p2Limit + c1Limit + c2Limit; wNum > 0; wNum-- {
		go work()
	}

	endSignal := taskPool
result_wait:
	for {
		select {
		case task := <-endEvent:
			sName := task.SectorName()
			fmt.Printf("done event:%s_%d, auto-release:%t\n", sName, task.Type, autoRelease)
			if err := os.RemoveAll(filepath.Join(sdir, "cache", sName)); err != nil {
				log.Warn(errors.As(err))
			}
			if err := os.RemoveAll(filepath.Join(sdir, "sealed", sName)); err != nil {
				log.Warn(errors.As(err))
			}
			if err := os.RemoveAll(filepath.Join(sdir, "unseal", sName)); err != nil {
				log.Warn(errors.As(err))
			}
			endSignal--
			if endSignal > 0 {
				continue result_wait
			}
			break result_wait

		case <-exit:
			cancel()
		case <-ctx.Done():
			// exit
			fmt.Println("user canceled")
			break result_wait
		}
	}

	// output the result
	fmt.Println("addpiece detail:")
	fmt.Println("=================")
	apSum, apMin, apMax := Statistics(apResult)
	fmt.Println()

	fmt.Println("precommit1 detail:")
	fmt.Println("=================")
	p1Sum, p1Min, p1Max := Statistics(p1Result)
	fmt.Println()

	fmt.Println("precommit2 detail:")
	fmt.Println("=================")
	p2Sum, p2Min, p2Max := Statistics(p2Result)
	fmt.Println()

	fmt.Println("commit1 detail:")
	fmt.Println("=================")
	c1Sum, c1Min, c1Max := Statistics(c1Result)
	fmt.Println()

	fmt.Println("commit2 detail:")
	fmt.Println("=================")
	c2Sum, c2Min, c2Max := Statistics(c2Result)
	fmt.Println()

	fmt.Printf(
		"total sectors:%d, order:%t, parallel-addpiece:%d, parallel-precommit1:%d, parallel-precommit2:%d, parallel-commit1:%d,parallel-commit2:%d\n",
		taskPool, orderRun, apLimit, p1Limit, p2Limit, c1Limit, c2Limit,
	)
	fmt.Println("=================")
	if apLimit <= 0 {
		return nil
	}
	fmt.Printf("addpiece    avg:%s, min:%s, max:%s\n", apSum/time.Duration(taskPool), apMin, apMax)
	if p1Limit <= 0 {
		return nil
	}
	fmt.Printf("precommit1 avg:%s, min:%s, max:%s\n", p1Sum/time.Duration(taskPool), p1Min, p1Max)
	if p2Limit <= 0 {
		return nil
	}
	fmt.Printf("precommit2 avg:%s, min:%s, max:%s\n", p2Sum/time.Duration(taskPool), p2Min, p2Max)
	if c1Limit <= 0 {
		return nil
	}
	fmt.Printf("commit1    avg:%s, min:%s, max:%s\n", c1Sum/time.Duration(taskPool), c1Min, c1Max)
	if c2Limit <= 0 {
		return nil
	}
	fmt.Printf("commit2    avg:%s, min:%s, max:%s\n", c2Sum/time.Duration(taskPool), c2Min, c2Max)

	return nil
}

func prepareTask(ctx context.Context, sb *Sealer, workerChan chan bool, taskChan chan *ParallelBenchTask, kind int, orderRun bool) {
	parallelLock.Lock()
	if !canParallel(kind, orderRun) {
		parallelLock.Unlock()
		time.Sleep(1e9)
		workerChan <- true // return the worker
		return
	}
	offsetParallelNum(kind, 1) // worker can have a task to work.
	parallelLock.Unlock()

	task := <-taskChan
	runTask(ctx, sb, task)

	parallelLock.Lock()
	offsetParallelNum(kind, -1)
	parallelLock.Unlock()
	workerChan <- true
}

func runTask(ctx context.Context, sb *Sealer, task *ParallelBenchTask) {
	sectorSize := task.SectorSize

	sid := SectorRef{
		SectorRef: storage.SectorRef{
			ID:        task.SectorID,
			ProofType: spt(sectorSize),
		},
		SectorFile: SectorFile{
			SectorId:     task.SectorName(),
			SealedRepo:   sb.RepoPath(),
			UnsealedRepo: sb.RepoPath(),
		},
	}
	switch task.Type {
	case TASK_KIND_ADDPIECE:
		startTime := time.Now()
		var pieceInfo []abi.PieceInfo
		r := rand.New(rand.NewSource(100 + int64(task.SectorID.Number)))
		pi, err := sb.AddPiece(ctx, sid.SectorRef, nil, abi.PaddedPieceSize(sectorSize).Unpadded(), r)
		if err != nil {
			panic(err) // failed
		}
		pieceInfo = []abi.PieceInfo{pi}
		resultLk.Lock()
		apResult = append(apResult, ParallelBenchResult{
			sectorName: task.SectorName(),
			startTime:  startTime,
			endTime:    time.Now(),
		})
		resultLk.Unlock()

		newTask := *task
		newTask.Type = TASK_KIND_PRECOMMIT1
		newTask.Pieces = pieceInfo
		if p1Limit == 0 {
			endEvent <- &newTask
		} else {
			p1TaskChan <- &newTask
		}
		return

	case TASK_KIND_PRECOMMIT1:
		startTime := time.Now()
		trand := blake2b.Sum256(task.TicketPreimage)
		ticket := abi.SealRandomness(trand[:])

		var pc1o storage.PreCommit1Out
		var err error
		pc1o, err = sb.SealPreCommit1(ctx, sid.SectorRef, ticket, task.Pieces)
		if err != nil {
			panic(err)
		}
		resultLk.Lock()
		p1Result = append(p1Result, ParallelBenchResult{
			sectorName: task.SectorName(),
			startTime:  startTime,
			endTime:    time.Now(),
		})
		resultLk.Unlock()

		newTask := *task
		newTask.Type = TASK_KIND_PRECOMMIT2
		newTask.PreCommit1Out = pc1o
		if p2Limit == 0 {
			endEvent <- &newTask
		} else {
			p2TaskChan <- &newTask
		}
		return
	case TASK_KIND_PRECOMMIT2:
		startTime := time.Now()
		var cids storage.SectorCids
		var err error

		//cids, err = sb.SealPreCommit2(ctx, sid.SectoreRef, task.PreCommit1Out)
		//if err != nil {
		//	panic(err)
		//}
		cids, err = ExecPrecommit2(ctx, task.Repo, WorkerTask{
			Sector: sid,

			// p2
			PreCommit1Out: task.PreCommit1Out,
		})
		if err != nil {
			panic(err)
		}

		resultLk.Lock()
		p2Result = append(p2Result, ParallelBenchResult{
			sectorName: task.SectorName(),
			startTime:  startTime,
			endTime:    time.Now(),
		})
		resultLk.Unlock()

		newTask := *task
		newTask.Type = TASK_KIND_COMMIT1
		newTask.Cids = cids
		if c1Limit == 0 {
			endEvent <- &newTask
		} else {
			c1TaskChan <- &newTask
		}
		return
	case TASK_KIND_COMMIT1:
		startTime := time.Now()
		seed := lapi.SealSeed{
			Epoch: 101,
			Value: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 255},
		}
		trand := blake2b.Sum256(task.TicketPreimage)
		ticket := abi.SealRandomness(trand[:])
		c1o, err := sb.SealCommit1(ctx, sid.SectorRef, ticket, seed.Value, task.Pieces, task.Cids)
		if err != nil {
			panic(err)
		}
		resultLk.Lock()
		c1Result = append(c1Result, ParallelBenchResult{
			sectorName: task.SectorName(),
			startTime:  startTime,
			endTime:    time.Now(),
		})
		resultLk.Unlock()

		newTask := *task
		newTask.Type = TASK_KIND_COMMIT2
		newTask.Commit1Out = c1o
		if c2Limit == 0 {
			endEvent <- &newTask
		} else {
			c2TaskChan <- &newTask
		}
		return

	case TASK_KIND_COMMIT2:
		startTime := time.Now()

		proof, err := sb.SealCommit2(ctx, sid.SectorRef, task.Commit1Out)
		if err != nil {
			panic(err)
		}
		// TODO: using c2 remote
		//	rpcClient, closer, err := NewOfflineC2RPC(ctx, "10.62.88.31:1287", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJBbGxvdyI6WyJyZWFkIiwid3JpdGUiLCJzaWduIiwiYWRtaW4iXX0.47G4EcZTeAwFPgmnnkBWWnNvy5wxk5_zqwNA2bNk7Nc")
		//	if err != nil {
		//		panic(err)
		//	}
		//	c2out, err := rpcClient.SealCommit2(ctx, sid.ID, []byte(task.Commit1Out))
		//	if err != nil {
		//		panic(err)
		//	}
		//	proof := Proof(c2out)
		//	closer() // release, TODO: make global rpc client

		resultLk.Lock()
		c2Result = append(c2Result, ParallelBenchResult{
			sectorName: task.SectorName(),
			startTime:  startTime,
			endTime:    time.Now(),
		})
		resultLk.Unlock()

		newTask := *task
		newTask.Type = 200
		newTask.Proof = proof
		endEvent <- &newTask
		return
	}
	panic(fmt.Sprintf("not reach here:%d", task.Type))
}
