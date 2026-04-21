package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	homePath   string
	dataDir    string
	backend    string
	app        string
	cosmosSdk  bool
	tendermint bool
	blocks     uint64
	versions   uint64
	tx_idx     bool
	compact    bool
	quiet      bool
	verbose    bool
	appName    = "cosmos-pruner"
)

// NewRootCmd returns the root command for relayer.
func NewRootCmd() *cobra.Command {
	// RootCmd represents the base command when called without any subcommands
	var rootCmd = &cobra.Command{
		Use:   appName,
		Short: "cosmos-pruner prunes data history from a Cosmos SDK / CometBFT node, avoiding the need to state-sync periodically",
	}

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		setLogLevel()
		return nil
	}

	rootCmd.PersistentFlags().Uint64VarP(&blocks, "blocks", "b", 10000, "amount of blocks to keep")
	if err := viper.BindPFlag("blocks", rootCmd.PersistentFlags().Lookup("blocks")); err != nil {
		panic(err)
	}

	rootCmd.PersistentFlags().Uint64VarP(&versions, "versions", "v", 10, "amount of versions to keep in the application store")
	if err := viper.BindPFlag("versions", rootCmd.PersistentFlags().Lookup("versions")); err != nil {
		panic(err)
	}

	rootCmd.PersistentFlags().StringVar(&backend, "backend", "goleveldb", "db backend (goleveldb, pebbledb, rocksdb)")
	if err := viper.BindPFlag("backend", rootCmd.PersistentFlags().Lookup("backend")); err != nil {
		panic(err)
	}

	rootCmd.PersistentFlags().StringVar(&app, "app", "", "app being pruned (supported: osmosis)")
	if err := viper.BindPFlag("app", rootCmd.PersistentFlags().Lookup("app")); err != nil {
		panic(err)
	}

	rootCmd.PersistentFlags().BoolVar(&cosmosSdk, "cosmos-sdk", true, "prune cosmos-sdk application state")
	if err := viper.BindPFlag("cosmos-sdk", rootCmd.PersistentFlags().Lookup("cosmos-sdk")); err != nil {
		panic(err)
	}

	rootCmd.PersistentFlags().BoolVar(&tendermint, "tendermint", true, "prune cometbft block and state stores")
	if err := viper.BindPFlag("tendermint", rootCmd.PersistentFlags().Lookup("tendermint")); err != nil {
		panic(err)
	}

	rootCmd.PersistentFlags().BoolVar(&tx_idx, "tx_index", true, "prune tx_index.db")

	rootCmd.PersistentFlags().BoolVar(&compact, "compact", true, "compact dbs after pruning")

	rootCmd.PersistentFlags().BoolVar(&quiet, "quiet", false, "suppress all non-error output")

	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", true, "enable detailed debug output")

	rootCmd.AddCommand(
		pruneCmd(),
	)

	return rootCmd
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.EnableCommandSorting = false

	rootCmd := NewRootCmd()
	rootCmd.SilenceUsage = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
