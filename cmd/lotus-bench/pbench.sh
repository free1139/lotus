#!/bin/sh

# REAME
# make bench
# nohup ./bench.sh &
# tail -f nohup.out
# REAME end

#size="64GiB" # 32GB
#size="32GiB" # 32GB
size="512MiB" # 512MB
#size="2KiB" # 2KB

#export IPFS_GATEWAY="https://proof-parameters.s3.cn-south-1.jdcloud-oss.com/ipfs/"
#export FIL_PROOFS_PARENT_CACHE="/data/cache/filecoin-parents"
#export FIL_PROOFS_PARAMETER_CACHE="/data/cache/filecoin-proof-parameters/v28" 

# Note that FIL_PROOFS_USE_GPU_TREE_BUILDER=1 is for tree_r_last building and FIL_PROOFS_USE_GPU_COLUMN_BUILDER=1 is for tree_c.  
# So be sure to use both if you want both built on the GPU
export FIL_PROOFS_USE_GPU_COLUMN_BUILDER=0
export FIL_PROOFS_USE_GPU_TREE_BUILDER=0 
#export FIL_PROOFS_MAX_GPU_COLUMN_BATCH_SIZE=X # X Y Z
export FIL_PROOFS_GPU_FRAMEWORK=cuda # opencl
#export BELLMAN_CUDA_NVCC_ARGS="--fatbin --gpu-architecture=sm_75 --generate-code=arch=compute_75,code=sm_75"
#export NEPTUNE_CUDA_NVCC_ARGS="--fatbin --gpu-architecture=sm_75 --generate-code=arch=compute_75,code=sm_75"
export FIL_PROOFS_MAXIMIZE_CACHING=1  # open cache for 32GB or 64GB
export FIL_PROOFS_USE_MULTICORE_SDR=1
export BELLMAN_NO_GPU=1

# export FIL_PROOFS_MULTICORE_SDR_PRODUCERS=3

# checking gpu
gpu=""
type nvidia-smi
if [ $? -eq 0 ]; then
    gpu=$(nvidia-smi -L|grep "GPU")
fi
if [ ! -z "$gpu" ]; then
    FIL_PROOFS_USE_GPU_COLUMN_BUILDER=1
    FIL_PROOFS_USE_GPU_TREE_BUILDER=1
    unset BELLMAN_NO_GPU
    export FIL_PROOFS_GPU_FRAMEWORK=cuda
    #export BELLMAN_CUSTOM_GPU="GeForce RTX 3090:10496" # for 460.91.03
    export BELLMAN_CUSTOM_GPU="NVIDIA GeForce RTX 3090:10496" # for 510.47.03
    export RUST_GPU_TOOLS_CUSTOM_GPU="NVIDIA GeForce RTX 3090:10496"
    #export BELLMAN_CUDA_NVCC_ARGS="--fatbin --gpu-architecture=sm_86 --generate-code=arch=compute_86,code=sm_86"
    #export BELLMAN_CUDA_NVCC_ARGS="--fatbin --gpu-architecture=sm_75 --generate-code=arch=compute_75,code=sm_75"
fi


RUST_LOG=info RUST_BACKTRACE=1 ./lotus-bench p-bench \
    --storage-dir=/data/cache/.lotus-bench \
    --sector-size=$size \
    --order=false \
    --auto-release=true \
    --task-pool=6 \
    --parallel-addpiece=2 \
    --parallel-precommit1=2 \
    --parallel-precommit2=1 \
    --parallel-commit1=0 \
    --parallel-commit2=0 &

pid=$!


# set ulimit for process
nropen=$(cat /proc/sys/fs/nr_open)
echo "max nofile limit:"$nropen
echo "current nofile of $pid limit:"$(cat /proc/$pid/limits|grep "open files")
#prlimit -p $pid --nofile=$nropen
prlimit -p $pid --nofile=655350 # it's better than $nropen
if [ $? -eq 0 ]; then
    echo "new nofile of $pid limit:"$(cat /proc/$pid/limits|grep "open files")
else
    echo "set prlimit failed, command: prlimit -p $pid --nofile=$nropen"
    exit 0
fi

wait $pid
echo ""
echo "bench end, size: "$size
echo ""
