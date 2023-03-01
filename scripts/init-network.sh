#!/usr/bin/env bash
export IPFS_GATEWAY="https://proof-parameters.s3.cn-south-1.jdcloud-oss.com/ipfs/"

# Note that FIL_PROOFS_USE_GPU_TREE_BUILDER=1 is for tree_r_last building and FIL_PROOFS_USE_GPU_COLUMN_BUILDER=1 is for tree_c.  
# So be sure to use both if you want both built on the GPU
export FIL_PROOFS_USE_GPU_COLUMN_BUILDER=0
export FIL_PROOFS_USE_GPU_TREE_BUILDER=0
export FIL_PROOFS_MAXIMIZE_CACHING=1  # open cache for 32GB or 64GB
export FIL_PROOFS_USE_MULTICORE_SDR=1

export RUST_LOG=info
export RUST_BACKTRACE=1

# env for build
export RUSTFLAGS="-C target-cpu=native -g" 
export CGO_CFLAGS="-D__BLST_PORTABLE__"
export FFI_BUILD_FROM_SOURCE=1
export SKIP_SEAL=0

# checking gpu
gpu=""
type nvidia-smi
if [ $? -eq 0 ]; then
    gpu=$(nvidia-smi -L|grep "GeForce")
fi
if [ ! -z "$gpu" ]; then
    FIL_PROOFS_USE_GPU_COLUMN_BUILDER=1
    FIL_PROOFS_USE_GPU_TREE_BUILDER=1
fi

set -xeo

NUM_SECTORS=1

SECTOR_SIZE=2048
#SECTOR_SIZE=536870912
#SECTOR_SIZE=34359738368
car_name="devnet.car"
build_mode="debug"
echo "SECTOR_SIZE:"$SECTOR_SIZE" mode:"$build_mode
touch build/genesis/devnet.car
touch build/bootstrap/devnet.pi


sdt0111=/data/lotus/dev/.sdt0111 # $(mktemp -d)

staging=/data/lotus/dev/.staging # $(mktemp -d)
mkdir -p $sdt0111
mkdir -p $staging

make $build_mode
if [ $SKIP_SEAL -eq 0 ]; then
    ./lotus-seed genesis new "${staging}/genesis.json"
    ./lotus-seed genesis set-signers --signers="t14po2vrupy7buror4g55c7shlcrmwsjxbpss7dzy" "${staging}/genesis.json"
    ./lotus-seed --sector-dir="${sdt0111}" pre-seal --sector-offset=0 --sector-size=${SECTOR_SIZE} --num-sectors=${NUM_SECTORS}
    ./lotus-seed genesis add-miner "${staging}/genesis.json" "${sdt0111}/pre-seal-t01000.json"
fi
ldt0111=/data/lotus/dev/.ldt0111 # $(mktemp -d)
rm -rf $ldt0111 && mkdir -p $ldt0111

lotus_path=$ldt0111
./lotus --repo="${lotus_path}" daemon --lotus-make-genesis="${staging}/devnet.car" --import-key="${sdt0111}/pre-seal-t01000.key" --genesis-template="${staging}/genesis.json" --bootstrap=false &
lpid=$!
sleep 120
kill $lpid
wait $lpid

mdt0111=/data/lotus/dev/.mdt0111 # $(mktemp -d)
rm -rf $mdt0111 && mkdir -p $mdt0111

# link the pre-seal data to repo
mkdir -p ${mdt0111}/cache
mkdir -p ${mdt0111}/sealed
mkdir -p ${mdt0111}/update-cache
mkdir -p ${mdt0111}/update
mkdir -p ${mdt0111}/deal-staging
mkdir -p ${mdt0111}/unsealed
for sector in `ls ${sdt0111}/cache`
do
    ln -s ${sdt0111}/cache/$sector ${mdt0111}/cache/$sector
done
for sector in `ls ${sdt0111}/sealed`
do
    ln -s ${sdt0111}/sealed/$sector ${mdt0111}/sealed/$sector
done
for sector in `ls ${sdt0111}/unsealed`
do
    ln -s ${sdt0111}/unsealed/$sector ${mdt0111}/unsealed/$sector
done

cp "${staging}/devnet.car" build/genesis/devnet.car
cp "${staging}/devnet.car" scripts/$car_name

make $build_mode

#./lotus --repo="${ldt0111}" daemon --genesis="${staging}/devnet.car" --api "3000" --bootstrap=false &
./lotus --repo="${ldt0111}" daemon --api "3000" --bootstrap=false &
lpid=$!
sleep 60

env LOTUS_PATH="${ldt0111}" LOTUS_MINER_PATH="${mdt0111}" ./lotus-miner init --genesis-miner --actor=t01000 --pre-sealed-sectors="${sdt0111}" --pre-sealed-metadata="${sdt0111}/pre-seal-t01000.json" --nosync=true --sector-size="${SECTOR_SIZE}"
sleep 10

kill $lpid
wait $lpid

echo "init done. using deploy-bootstrap.sh to deploy the daemons"
