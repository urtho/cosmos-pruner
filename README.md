# Cosmos-Pruner

This is a fork of [cosmprund](https://github.com/binaryholdings/cosmprund) with some improvements:
- support pebbledb
- prune all stores of app state
- support blockstore prunning
- support tx_index prunning


## Usage

```
# clone & build cosmos-pruner repo
git clone https://github.com/notional-labs/cosmprund
cd cosmprund
make build

# run cosmos-pruner
./build/cosmos-pruner prune ~/.gaiad/data --app=cosmoshub --backend=pebbledb --blocks=362880 --versions=362880 --compact=true
```

Flags: 

- `data-dir`: path to data directory if not d efault
- `blocks`: amount of blocks to keep on the node (Default 1000)
- `versions`: amount of app state versions to keep on the node (Default 10)
- `app`: deprecated! does not use for anything exccep some special chains.
- `cosmos-sdk`: If pruning app state (Default true)
- `tendermint`: If pruning tendermint data including blockstore and state. (Default true)
- `tx_index`: set to false you dont want to prune tx_index.db (default true)
- `compact`: set to false you dont want to compact dbs after prunning (default true)
