#!/bin/sh

install_path=$HOME/fil-miner/apps/lotus;
if [ ! -z "$FILECOIN_BIN" ]; then
    install_path=$FILECOIN_BIN
fi
mkdir -p $install_path

# set the build path for cert
certfrom="scripts"
if [ ! -z $FILECOIN_CERT ]; then
    certfrom=$FILECOIN_CERT
fi

# env for build
export FFI_BUILD_FROM_SOURCE=1
export RUSTFLAGS="-C target-cpu=native -g" 
export CGO_CFLAGS="-D__BLST_PORTABLE__"
export CGO_CFLAGS_ALLOW="-D__BLST_PORTABLE__"
export FFI_USE_CUDA=1

echo "make "$1
case $1 in
    "debug")
	#unset RUSTFLAGS
	#unset CGO_CFLAGS
	#unset CGO_CFLAGS_ALLOW
	unset FFI_USE_CUDA
        cp -v scripts/devnet.pi build/bootstrap/devnet.pi
        cp -v scripts/devnet.car build/genesis/devnet.car
        make debug
    ;;
    "calibration"|"calibnet")
        make calibnet
    ;;
    *)
        make $1
    ;;
esac
# roolback build resource
git checkout ./build

echo "copy bin to "$install_path
if [ -f ./lotus ]; then
    cp -vrf lotus $install_path
fi
if [ -f ./lotus-miner ]; then
    cp -vrf lotus-miner $install_path
fi
if [ -f ./lotus-worker ]; then
    cp -vrf lotus-worker $install_path
fi
if [ -f ./lotus-shed ]; then
    cp -vrf lotus-shed $install_path
fi
