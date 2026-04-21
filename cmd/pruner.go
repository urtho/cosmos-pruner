package cmd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cosmossdk.io/log"
	"cosmossdk.io/store/iavl"
	"cosmossdk.io/store/metrics"
	"cosmossdk.io/store/rootmulti"
	storetypes "cosmossdk.io/store/types"
	"github.com/cockroachdb/pebble"
	cmtdb "github.com/cometbft/cometbft-db"
	cmtstate "github.com/cometbft/cometbft/state"
	cmtstore "github.com/cometbft/cometbft/store"
	cosmosdb "github.com/cosmos/cosmos-db"
	iavltree "github.com/cosmos/iavl"
	"github.com/spf13/cobra"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

const pruneBatchSize = 10000

// txIdxHeight is the height below which tx/block indexer data will be pruned.
var txIdxHeight int64 = 0

func pruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [path_to_home]",
		Short: "prune data from the application store and block store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home := args[0]

			if tendermint {
				if err := pruneTMData(home); err != nil {
					logErr("tendermint pruning failed: %s", err.Error())
				}
			}

			if cosmosSdk {
				if err := pruneAppState(home); err != nil {
					logErr("app state pruning failed: %s", err.Error())
				}
			}

			if tx_idx {
				if err := pruneTxIndex(home); err != nil {
					logErr("tx_index pruning failed: %s", err.Error())
				}
			}

			return nil
		},
	}
	return cmd
}

// --- App state pruning ---

type appPlan struct {
	latest     int64
	pruneTo    int64
	storeNames []string
}

// inspectAppState performs sanity checks on the application DB and returns a plan.
// Returns (nil, nil) if there is nothing to prune.
func inspectAppState(appDB cosmosdb.DB) (*appPlan, error) {
	latest := getLatestVersion(appDB)
	if latest == 0 {
		logWarn("application store: no commit info found, nothing to prune")
		return nil, nil
	}

	storeNames, err := readStoreNames(appDB, latest)
	if err != nil {
		return nil, fmt.Errorf("read commit info: %w", err)
	}
	if len(storeNames) == 0 {
		logWarn("application store: no substores found, nothing to prune")
		return nil, nil
	}

	pruneTo := latest - int64(versions)
	logInfo("application store: latest=%d, keep=%d, substores=%d",
		latest, versions, len(storeNames))
	if pruneTo <= 0 {
		logInfo("application store: not enough versions to prune (pruneTo=%d)", pruneTo)
		return nil, nil
	}
	logInfo("application store: will prune versions [1..%d], keep [%d..%d]",
		pruneTo, pruneTo+1, latest)

	if currentLevel >= levelDebug {
		for _, name := range storeNames {
			ver := readStoreStorageVersion(appDB, name)
			if ver == "" {
				logDebug("  substore %q: storage_version=<unset> (legacy iavl format)", name)
			} else {
				logDebug("  substore %q: storage_version=%s", name, ver)
			}
		}
	}

	return &appPlan{latest: latest, pruneTo: pruneTo, storeNames: storeNames}, nil
}

func pruneAppState(home string) error {
	if app == "osmosis" {
		logInfo("application store: osmosis app state pruning not supported, skipping")
		return nil
	}

	if !dbExists(home, "application") {
		logWarn("application store: application.db not found, skipping")
		return nil
	}

	appDB, err := openCosmosDB("application", home)
	if err != nil {
		return err
	}
	defer appDB.Close()

	plan, err := inspectAppState(appDB)
	if err != nil {
		return err
	}
	if plan == nil {
		return nil
	}

	if txIdxHeight <= 0 {
		txIdxHeight = plan.latest
		logDebug("set txIdxHeight=%d from app latest version", txIdxHeight)
	}

	logger := log.NewNopLogger()
	rs := rootmulti.NewStore(appDB, logger, metrics.NewNoOpMetrics())
	keys := make(map[string]*storetypes.KVStoreKey, len(plan.storeNames))
	for _, name := range plan.storeNames {
		k := storetypes.NewKVStoreKey(name)
		keys[name] = k
		rs.MountStoreWithDB(k, storetypes.StoreTypeIAVL, nil)
	}

	if err := rs.LoadLatestVersion(); err != nil {
		return fmt.Errorf("LoadLatestVersion failed (store may need iavl-v1 upgrade — start the chain binary once to migrate): %w", err)
	}

	logInfo("application store: pruning %d substores", len(plan.storeNames))
	for i, name := range plan.storeNames {
		store := rs.GetCommitKVStore(keys[name])
		iavlStore, ok := store.(*iavl.Store)
		if !ok {
			logDebug("  [%d/%d] %q: not IAVL, skipping", i+1, len(plan.storeNames), name)
			continue
		}
		logInfo("  [%d/%d] pruning substore %q to version %d",
			i+1, len(plan.storeNames), name, plan.pruneTo)
		if err := iavlStore.DeleteVersionsTo(plan.pruneTo); err != nil {
			if !errors.Is(err, iavltree.ErrVersionDoesNotExist) {
				logErr("  substore %q prune error: %s", name, err.Error())
			}
		}
	}

	if compact {
		logInfo("application store: compacting")
		if err := compactCosmosDB(appDB); err != nil {
			logErr("compact failed: %s", err.Error())
		}
	}

	return nil
}

// --- CometBFT block + state pruning ---

type tmPlan struct {
	base         int64
	height       int64
	pruneTo      int64
	state        cmtstate.State
	evidenceThld int64
}

func inspectTMData(blockStore *cmtstore.BlockStore, stateStore cmtstate.Store) (*tmPlan, error) {
	state, err := stateStore.Load()
	if err != nil {
		return nil, fmt.Errorf("state load: %w", err)
	}

	base := blockStore.Base()
	height := blockStore.Height()
	logInfo("blockstore: base=%d, height=%d, keep=%d", base, height, blocks)

	if height == 0 {
		logWarn("blockstore: height is 0, nothing to prune")
		return nil, nil
	}

	pruneTo := height - int64(blocks)
	if pruneTo <= base {
		logInfo("blockstore: nothing to prune (pruneTo=%d <= base=%d)", pruneTo, base)
		return nil, nil
	}

	logInfo("blockstore: will prune blocks [%d..%d], keep [%d..%d]",
		base, pruneTo-1, pruneTo, height)
	logInfo("statestore: will prune states  [%d..%d]", base, pruneTo-1)

	thld := evidenceThresholdHeight(state)
	logDebug("evidence threshold height: %d (LastBlockHeight=%d, MaxAgeNumBlocks=%d)",
		thld, state.LastBlockHeight, state.ConsensusParams.Evidence.MaxAgeNumBlocks)

	return &tmPlan{
		base:         base,
		height:       height,
		pruneTo:      pruneTo,
		state:        state,
		evidenceThld: thld,
	}, nil
}

func pruneTMData(home string) error {
	if !dbExists(home, "blockstore") {
		logWarn("blockstore.db not found, skipping cometbft pruning")
		return nil
	}
	if !dbExists(home, "state") {
		logWarn("state.db not found, skipping cometbft pruning")
		return nil
	}

	blockStoreDB, err := openCmtDB("blockstore", home)
	if err != nil {
		return err
	}
	defer blockStoreDB.Close()

	blockStore := cmtstore.NewBlockStore(blockStoreDB)

	stateDB, err := openCmtDB("state", home)
	if err != nil {
		return err
	}
	defer stateDB.Close()

	stateStore := cmtstate.NewStore(stateDB, cmtstate.StoreOptions{DiscardABCIResponses: false})
	defer stateStore.Close()

	plan, err := inspectTMData(blockStore, stateStore)
	if err != nil {
		return err
	}
	if plan == nil {
		return nil
	}

	if txIdxHeight <= 0 {
		txIdxHeight = plan.height
		logDebug("set txIdxHeight=%d from blockstore", txIdxHeight)
	}

	totalBlocks := plan.pruneTo - plan.base
	logInfo("blockstore: pruning %d blocks in batches of %d", totalBlocks, pruneBatchSize)
	for from := plan.base; from < plan.pruneTo-1; from += pruneBatchSize {
		target := from + pruneBatchSize
		if target > plan.pruneTo-1 {
			target = plan.pruneTo - 1
		}
		if _, _, err := blockStore.PruneBlocks(target, plan.state); err != nil {
			logErr("blockstore prune error at %d: %s", target, err.Error())
		}
		done := target - plan.base
		logInline("  blockstore: %d / %d (%.1f%%)", done, totalBlocks, pct(done, totalBlocks))
	}
	logInlineEnd()

	if compact {
		logInfo("blockstore: compacting")
		if err := compactCmtDB(blockStoreDB); err != nil {
			logErr("compact failed: %s", err.Error())
		}
	}

	totalStates := plan.pruneTo - plan.base
	logInfo("statestore: pruning %d states in batches of %d", totalStates, pruneBatchSize)
	for from := plan.base; from < plan.pruneTo-1; from += pruneBatchSize {
		to := from + pruneBatchSize
		if to > plan.pruneTo-1 {
			to = plan.pruneTo - 1
		}
		if from >= to {
			break
		}
		if err := stateStore.PruneStates(from, to, plan.evidenceThld); err != nil {
			logErr("statestore prune error [%d..%d]: %s", from, to, err.Error())
		}
		done := to - plan.base
		logInline("  statestore: %d / %d (%.1f%%)", done, totalStates, pct(done, totalStates))
	}
	logInlineEnd()

	if compact {
		logInfo("statestore: compacting")
		if err := compactCmtDB(stateDB); err != nil {
			logErr("compact failed: %s", err.Error())
		}
	}

	return nil
}

// --- Tx index pruning ---

func pruneTxIndex(home string) error {
	if !dbExists(home, "tx_index") {
		logWarn("tx_index.db not found, skipping tx_index pruning")
		return nil
	}

	pruneHeight := txIdxHeight - int64(blocks) - 10
	logInfo("tx_index: txIdxHeight=%d, keep=%d, pruneTo=%d", txIdxHeight, blocks, pruneHeight)
	if pruneHeight <= 0 {
		logInfo("tx_index: nothing to prune (pruneTo=%d)", pruneHeight)
		return nil
	}

	txIdxDB, err := openCmtDB("tx_index", home)
	if err != nil {
		return err
	}
	defer txIdxDB.Close()

	logInfo("tx_index: pruning block index entries below %d", pruneHeight)
	deleted, kept := pruneBlockIndex(txIdxDB, pruneHeight)
	logInfo("tx_index: block index — deleted %d, kept %d entries", deleted, kept)

	logInfo("tx_index: pruning tx index entries below %d", pruneHeight)
	deleted, kept = pruneTxIndexTxs(txIdxDB, pruneHeight)
	logInfo("tx_index: tx index — deleted %d, kept %d entries", deleted, kept)

	if compact {
		logInfo("tx_index: compacting")
		if err := compactCmtDB(txIdxDB); err != nil {
			logErr("compact failed: %s", err.Error())
		}
	}

	return nil
}

func pruneTxIndexTxs(db cmtdb.DB, pruneHeight int64) (int64, int64) {
	itr, err := db.Iterator(nil, nil)
	if err != nil {
		panic(err)
	}
	defer itr.Close()

	bat := db.NewBatch()
	counter := 0
	var deleted, kept int64

	for ; itr.Valid(); itr.Next() {
		key := itr.Key()
		value := itr.Value()
		strKey := string(key)
		deletedThis := false

		if strings.HasPrefix(strKey, "tx.height") {
			parts := strings.Split(strKey, "/")
			intHeight, _ := strconv.ParseInt(parts[2], 10, 64)
			if intHeight < pruneHeight {
				_ = bat.Delete(value)
				_ = bat.Delete(key)
				counter += 2
				deletedThis = true
			}
		} else if len(value) == 32 {
			parts := strings.Split(strKey, "/")
			if len(parts) == 4 {
				intHeight, _ := strconv.ParseInt(parts[2], 10, 64)
				if intHeight < pruneHeight {
					_ = bat.Delete(key)
					counter++
					deletedThis = true
				}
			}
		}
		if !deletedThis {
			kept++
		}

		if counter >= 100000 {
			_ = bat.WriteSync()
			deleted += int64(counter)
			logInline("  tx_index batch flushed: %d (deleted: %d, kept: %d)", counter, deleted, kept)
			counter = 0
			_ = bat.Close()
			bat = db.NewBatch()
		}
	}

	deleted += int64(counter)
	_ = bat.WriteSync()
	_ = bat.Close()
	logInlineEnd()
	return deleted, kept
}

func pruneBlockIndex(db cmtdb.DB, pruneHeight int64) (int64, int64) {
	itr, err := db.Iterator(nil, nil)
	if err != nil {
		panic(err)
	}
	defer itr.Close()

	bat := db.NewBatch()
	counter := 0
	var deleted, kept int64

	for ; itr.Valid(); itr.Next() {
		key := itr.Key()
		value := itr.Value()
		strKey := string(key)
		deletedThis := false

		if strings.HasPrefix(strKey, "block.height") || strings.HasPrefix(strKey, "block_events") {
			intHeight := int64FromBytes(value)
			if intHeight < pruneHeight {
				_ = bat.Delete(key)
				counter++
				deletedThis = true
			}
		}
		if !deletedThis {
			kept++
		}

		if counter >= 100000 {
			_ = bat.WriteSync()
			deleted += int64(counter)
			logInline("  block_index batch flushed: %d (deleted: %d, kept: %d)", counter, deleted, kept)
			counter = 0
			_ = bat.Close()
			bat = db.NewBatch()
		}
	}

	deleted += int64(counter)
	_ = bat.WriteSync()
	_ = bat.Close()
	logInlineEnd()
	return deleted, kept
}

// --- Sanity / utility ---

func dbExists(home, name string) bool {
	dbDir := rootify(dataDir, home)
	path := filepath.Join(dbDir, name+".db")
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// dbOpts implements cosmos-db's Options interface (used to pass maxopenfiles).
type dbOpts map[string]interface{}

func (o dbOpts) Get(k string) interface{} { return o[k] }

// openCosmosDB opens a database using the cosmos-db library (for application state).
func openCosmosDB(name, home string) (cosmosdb.DB, error) {
	dbType := cosmosdb.BackendType(backend)
	dbDir := rootify(dataDir, home)

	switch dbType {
	case cosmosdb.GoLevelDBBackend:
		o := &opt.Options{DisableSeeksCompaction: true, OpenFilesCacheCapacity: 100}
		return cosmosdb.NewGoLevelDBWithOpts(name, dbDir, o)
	case cosmosdb.PebbleDBBackend:
		return cosmosdb.NewDBwithOptions(name, dbType, dbDir, dbOpts{"maxopenfiles": 100})
	default:
		return cosmosdb.NewDB(name, dbType, dbDir)
	}
}

// openCmtDB opens a database using the cometbft-db library (for blockstore/state/tx_index).
func openCmtDB(name, home string) (cmtdb.DB, error) {
	dbType := cmtdb.BackendType(backend)
	dbDir := rootify(dataDir, home)

	switch dbType {
	case cmtdb.GoLevelDBBackend:
		o := &opt.Options{DisableSeeksCompaction: true, OpenFilesCacheCapacity: 100}
		return cmtdb.NewGoLevelDBWithOpts(name, dbDir, o)
	case cmtdb.PebbleDBBackend:
		opts := &pebble.Options{MaxOpenFiles: 100}
		opts.EnsureDefaults()
		return cmtdb.NewPebbleDBWithOpts(name, dbDir, opts)
	default:
		return cmtdb.NewDB(name, dbType, dbDir)
	}
}

func compactCosmosDB(db cosmosdb.DB) error {
	switch vdb := db.(type) {
	case *cosmosdb.GoLevelDB:
		return vdb.ForceCompact(nil, nil)
	case *cosmosdb.PebbleDB:
		return compactPebble(vdb.DB())
	}
	return nil
}

func compactCmtDB(db cmtdb.DB) error {
	switch vdb := db.(type) {
	case *cmtdb.GoLevelDB:
		return vdb.Compact(nil, nil)
	case *cmtdb.PebbleDB:
		return compactPebble(vdb.DB())
	}
	return nil
}

func compactPebble(p *pebble.DB) error {
	iter, err := p.NewIter(nil)
	if err != nil {
		return err
	}

	var start, end []byte
	if iter.First() {
		start = cp(iter.Key())
	}
	if iter.Last() {
		end = cp(iter.Key())
	}
	if err := iter.Close(); err != nil {
		return err
	}
	if start == nil || end == nil {
		return nil
	}
	return p.Compact(start, end, false)
}

// readStoreNames loads the committed store names at the given version from the application db.
func readStoreNames(db cosmosdb.DB, ver int64) ([]string, error) {
	cInfoKey := fmt.Sprintf("s/%d", ver)
	bz, err := db.Get([]byte(cInfoKey))
	if err != nil {
		return nil, fmt.Errorf("failed to get commit info: %w", err)
	}
	if bz == nil {
		return nil, fmt.Errorf("no commit info found for version %d", ver)
	}

	cInfo := &storetypes.CommitInfo{}
	if err := cInfo.Unmarshal(bz); err != nil {
		return nil, fmt.Errorf("failed to unmarshal commit info: %w", err)
	}

	names := make([]string, 0, len(cInfo.StoreInfos))
	for _, si := range cInfo.StoreInfos {
		names = append(names, si.Name)
	}
	return names, nil
}

// readStoreStorageVersion returns the iavl on-disk storage version for a substore,
// or "" if the metadata key isn't present (legacy pre-fast-storage iavl).
// iavl key format: 'm' prefix + "storage_version" (see iavl/keyformat).
func readStoreStorageVersion(db cosmosdb.DB, storeName string) string {
	prefixed := cosmosdb.NewPrefixDB(db, []byte("s/k:"+storeName+"/"))
	bz, err := prefixed.Get([]byte("mstorage_version"))
	if err != nil || bz == nil {
		return ""
	}
	return string(bz)
}

// getLatestVersion reads the latest committed version from the rootmulti store's metadata.
func getLatestVersion(db cosmosdb.DB) int64 {
	bz, err := db.Get([]byte("s/latest"))
	if err != nil {
		panic(err)
	}
	if bz == nil {
		return 0
	}

	// The latest version is stored as a gogoproto Int64Value:
	// payload is prefixed with 1-byte tag (0x08) followed by a varint.
	if len(bz) >= 2 && bz[0] == 0x08 {
		v, _ := binary.Varint(bz[1:])
		return v
	}
	v, _ := binary.Varint(bz)
	return v
}

func evidenceThresholdHeight(state cmtstate.State) int64 {
	if state.ConsensusParams.Evidence.MaxAgeNumBlocks <= 0 {
		return 0
	}
	t := state.LastBlockHeight - state.ConsensusParams.Evidence.MaxAgeNumBlocks
	if t < 0 {
		t = 0
	}
	return t
}

func cp(bz []byte) []byte {
	out := make([]byte, len(bz))
	copy(out, bz)
	return out
}

func rootify(path, root string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func int64FromBytes(bz []byte) int64 {
	v, _ := binary.Varint(bz)
	return v
}
