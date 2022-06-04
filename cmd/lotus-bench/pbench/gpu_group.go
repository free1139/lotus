package pbench

import (
	"context"
	"encoding/xml"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gwaylib/errors"
)

type GpuPci struct {
	PciBus   string `xml:"pci_bus"`
	PciBusID string `xml:"pci_bus_id"`
	// TODO: more infomation
}

func (p *GpuPci) ParseBusId() (int, error) {
	val, err := strconv.ParseInt(p.PciBus, 16, 32)
	if err != nil {
		return 0, err
	}
	return int(val), nil
}

type GpuInfo struct {
	UUID string `xml:"uuid"`
	Pci  GpuPci `xml:"pci"`
	// TODO: more infomation
}

func (info *GpuInfo) UniqueID() string {
	return strings.Replace(info.UUID, "GPU-", "", 1)
}

type GpuXml struct {
	XMLName xml.Name  `xml:"nvidia_smi_log"`
	Gpu     []GpuInfo `xml:"gpu"`
	// TODO: more infomation
}

func GroupGpu(ctx context.Context) ([]GpuInfo, error) {
	input, err := exec.CommandContext(ctx, "nvidia-smi", "-q", "-x").CombinedOutput()
	if err != nil {
		return nil, errors.As(err)
	}
	output := GpuXml{}
	if err := xml.Unmarshal(input, &output); err != nil {
		return nil, errors.As(err)
	}
	return output.Gpu, nil
}

var (
	gpuGroup  = []GpuInfo{}
	gpuKeys   = map[int]bool{}
	gpuLock   = sync.Mutex{}
	gpuInited = time.Time{}
)

func initGpuGroup(ctx context.Context) error {
	gpuLock.Lock()
	defer gpuLock.Unlock()
	now := time.Now()
	if now.Sub(gpuInited) > time.Minute {
		gpuInited = now
		group, err := GroupGpu(ctx)
		if err != nil {
			return errors.As(err)
		}
		gpuGroup = group
	}
	return nil
}

// TODO: limit call frequency
func hasGPU(ctx context.Context) bool {
	gpuLock.Lock()
	defer gpuLock.Unlock()
	gpus, err := GroupGpu(ctx)
	if err != nil {
		return false
	}
	return len(gpus) > 0
}

func needGPU() bool {
	return os.Getenv("BELLMAN_NO_GPU") != "1" && os.Getenv("FIL_PROOFS_GPU_MODE") == "force"
}

func allocateGPU(ctx context.Context) (int, *GpuInfo, error) {
	if err := initGpuGroup(ctx); err != nil {
		return 0, nil, errors.As(err)
	}

	gpuLock.Lock()
	defer gpuLock.Unlock()
	for i, gpuInfo := range gpuGroup {
		using, _ := gpuKeys[i]
		if using {
			continue
		}
		gpuKeys[i] = true
		return i, &gpuInfo, nil
	}
	if len(gpuGroup) == 0 {
		// using cpu when no gpu hardware.
		return 0, &GpuInfo{}, nil
	}
	return 0, nil, errors.New("allocate gpu failed").As(gpuKeys, gpuGroup)
}

func syncAllocateGPU(ctx context.Context) (int, *GpuInfo) {
	for {
		idx, group, err := allocateGPU(ctx)
		if err != nil {
			log.Warn("allocate gpu failed, retry 1s later. ", errors.As(err))
			time.Sleep(1e9)
			continue
		}
		return idx, group
	}
}

func returnGPU(key int) {
	gpuLock.Lock()
	defer gpuLock.Unlock()
	if key < 0 {
		return
	}
	gpuKeys[key] = false
}

func AssertGPU(ctx context.Context) {
	// assert gpu for release mode
	// only the develop mode don't need gpu
	if needGPU() && !hasGPU(ctx) {
		log.Fatalf("os exit by gpu not found(BELLMAN_NO_GPU=%s, FIL_PROOFS_GPU_MODE=%s)", os.Getenv("BELLMAN_NO_GPU"), os.Getenv("FIL_PROOFS_GPU_MODE"))
		time.Sleep(3e9)
		os.Exit(-1)
	}
}
