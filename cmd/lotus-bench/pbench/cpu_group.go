package pbench

import (
	"bytes"
	"context"
	"encoding/csv"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gwaylib/errors"
)

type CpuCache struct {
	Cpu int
	L1d int
	L1i int
	L2  int
	L3  int
}

func GetTaskSetKey(group [][]int) map[int]bool {
	keys := map[int]bool{}
	for i, _ := range group {
		keys[i] = false
	}
	return keys
}

func GroupCpuCache(ctx context.Context) ([][]int, error) {
	out, err := exec.CommandContext(ctx, "lscpu", "-p=CPU,CACHE").CombinedOutput()
	if err != nil {
		return nil, errors.As(err)
	}
	r := csv.NewReader(bytes.NewReader(out))
	r.Comma = ','
	r.Comment = '#'
	records, err := r.ReadAll()
	if err != nil {
		return nil, errors.As(err)
	}

	group := [][]int{}
	cache := []int{}
	cacheIndex := -1
	for _, r := range records {
		if len(r) != 2 {
			return nil, errors.New("error cpu format").As(records)
		}
		cpu, err := strconv.Atoi(r[0])
		if err != nil {
			return nil, errors.As(err)
		}
		caches := strings.Split(r[1], ":")
		if len(caches) < 4 {
			return nil, errors.New("error cache format").As(records)
		}
		l3, err := strconv.Atoi(caches[3])
		if err != nil {
			return nil, errors.New("error cache format").As(records)
		}
		if l3 != cacheIndex {
			if len(cache) > 0 {
				group = append(group, cache)
			}
			cache = []int{cpu}
			cacheIndex = l3
		} else {
			cache = append(cache, cpu)
		}
	}
	if len(cache) > 0 {
		group = append(group, cache)
	}
	return group, nil
}

var (
	cpuGroup = [][]int{}
	cpuKeys  = map[int]bool{}
	cpuLock  = sync.Mutex{}
)

func initCpuGroup(ctx context.Context) error {
	cpuLock.Lock()
	defer cpuLock.Unlock()
	if len(cpuKeys) == 0 {
		group, err := GroupCpuCache(ctx)
		if err != nil {
			return errors.As(err)
		}
		cpuKeys = GetTaskSetKey(group)
		cpuGroup = group
	}
	return nil
}

func allocateCpu(ctx context.Context, aesc bool) (int, []int, error) {
	if err := initCpuGroup(ctx); err != nil {
		return -1, nil, errors.As(err)
	}

	cpuLock.Lock()
	defer cpuLock.Unlock()

	cpuNum := len(cpuGroup)
	if !aesc {
		for i := 0; i < cpuNum; i++ {
			cpus := cpuGroup[i]
			using, _ := cpuKeys[i]
			if using {
				continue
			}
			cpuKeys[i] = true
			return i, cpus, nil
		}
	} else {
		for i := cpuNum - 1; i > -1; i-- {
			cpus := cpuGroup[i]
			using, _ := cpuKeys[i]
			if using {
				continue
			}
			cpuKeys[i] = true
			return i, cpus, nil
		}
	}
	return -1, nil, errors.New("allocate cpu failed").As(len(cpuKeys))
}

func syncAllocateCpu(ctx context.Context, aesc bool) (int, []int) {
	for {
		idx, group, err := allocateCpu(ctx, false)
		if err != nil {
			log.Warn("allocate cpu failed, retry 1s later", errors.As(err))
			time.Sleep(1e9)
			continue
		}
		return idx, group
	}
}

func returnCpu(idx int) {
	cpuLock.Lock()
	defer cpuLock.Unlock()
	cpuKeys[idx] = false
}
