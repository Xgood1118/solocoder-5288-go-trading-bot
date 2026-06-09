package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"trading-bot/internal/backtest"
	"trading-bot/internal/live"
	"trading-bot/internal/market"
	"trading-bot/internal/order"
	"trading-bot/internal/position"
	"trading-bot/internal/report"
	"trading-bot/internal/risk"
	"trading-bot/internal/strategy"
	"trading-bot/pkg/types"
	"trading-bot/web"
)

var (
	Version   = "1.0.0"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

type Config struct {
	Database   DatabaseConfig   `yaml:"database"`
	Web        WebConfig        `yaml:"web"`
	Market     MarketConfig     `yaml:"market"`
	Risk       RiskConfig       `yaml:"risk"`
	Strategies []StrategyConfig `yaml:"strategies"`
	Logging    LoggingConfig    `yaml:"logging"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type WebConfig struct {
	Address string `yaml:"address"`
	Enabled bool   `yaml:"enabled"`
}

type MarketConfig struct {
	Symbol       string `yaml:"symbol"`
	InitialPrice int64  `yaml:"initial_price"`
	Amplitude    int64  `yaml:"amplitude"`
	Period       string `yaml:"period"`
	Frequency    string `yaml:"frequency"`
}

type RiskConfig struct {
	MaxSingleOrderValue int64     `yaml:"max_single_order_value"`
	MaxPositionValue    int64     `yaml:"max_position_value"`
	DailyLossLimit      int64     `yaml:"daily_loss_limit"`
	CircuitBreakerPct   float64   `yaml:"circuit_breaker_pct"`
	DisabledSymbols     []string  `yaml:"disabled_symbols"`
}

type StrategyConfig struct {
	ID      string                 `yaml:"id"`
	Name    string                 `yaml:"name"`
	Type    string                 `yaml:"type"`
	Symbol  string                 `yaml:"symbol"`
	Enabled bool                   `yaml:"enabled"`
	Params  map[string]interface{} `yaml:"params"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "backtest":
		backtestCmd(os.Args[2:])
	case "strategies":
		strategiesCmd(os.Args[2:])
	case "serve":
		serveCmd(os.Args[2:])
	case "version":
		versionCmd(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: bot <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  run         Start live trading bot")
	fmt.Println("  backtest    Run backtest with specified strategy")
	fmt.Println("  strategies  List available strategies")
	fmt.Println("  serve       Start web dashboard only")
	fmt.Println("  version     Show version information")
	fmt.Println()
	fmt.Println("Use 'bot <command> --help' for more information about a command.")
}

func loadConfig(configPath string) (*Config, error) {
	if configPath == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			configPath = filepath.Join(homeDir, ".trading-bot", "config.yaml")
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.Database.Path == "" {
		cfg.Database.Path = "trading-bot.db"
	}
	if cfg.Web.Address == "" {
		cfg.Web.Address = ":8080"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
	if cfg.Logging.Output == "" {
		cfg.Logging.Output = "stdout"
	}

	return &cfg, nil
}

func setupLogger(cfg LoggingConfig) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}

	var output *os.File
	switch strings.ToLower(cfg.Output) {
	case "stderr":
		output = os.Stderr
	default:
		output = os.Stdout
	}

	if strings.ToLower(cfg.Format) == "json" {
		handler = slog.NewJSONHandler(output, opts)
	} else {
		handler = slog.NewTextHandler(output, opts)
	}

	return slog.New(handler)
}

func createStrategy(cfg StrategyConfig) (types.Strategy, error) {
	switch strings.ToLower(cfg.Type) {
	case "ma_crossover":
		fastPeriod := 5
		slowPeriod := 20
		if v, ok := cfg.Params["fast_period"]; ok {
			fastPeriod = int(toFloat64(v))
		}
		if v, ok := cfg.Params["slow_period"]; ok {
			slowPeriod = int(toFloat64(v))
		}
		return strategy.NewMACrossover(cfg.ID, cfg.Symbol, strategy.MACrossoverConfig{
			FastPeriod: fastPeriod,
			SlowPeriod: slowPeriod,
		}), nil

	case "rsi":
		period := 14
		overbought := 70.0
		oversold := 30.0
		if v, ok := cfg.Params["period"]; ok {
			period = int(toFloat64(v))
		}
		if v, ok := cfg.Params["overbought"]; ok {
			overbought = toFloat64(v)
		}
		if v, ok := cfg.Params["oversold"]; ok {
			oversold = toFloat64(v)
		}
		return strategy.NewRSI(cfg.ID, cfg.Symbol, strategy.RSIConfig{
			Period:     period,
			Overbought: overbought,
			Oversold:   oversold,
		}), nil

	case "macd":
		fastEMAPeriod := 12
		slowEMAPeriod := 26
		signalPeriod := 9
		if v, ok := cfg.Params["fast_ema_period"]; ok {
			fastEMAPeriod = int(toFloat64(v))
		}
		if v, ok := cfg.Params["slow_ema_period"]; ok {
			slowEMAPeriod = int(toFloat64(v))
		}
		if v, ok := cfg.Params["signal_period"]; ok {
			signalPeriod = int(toFloat64(v))
		}
		return strategy.NewMACD(cfg.ID, cfg.Symbol, strategy.MACDConfig{
			FastEMAPeriod: fastEMAPeriod,
			SlowEMAPeriod: slowEMAPeriod,
			SignalPeriod:  signalPeriod,
		}), nil

	case "bollinger":
		period := 20
		stdDevTimes := 2.0
		if v, ok := cfg.Params["period"]; ok {
			period = int(toFloat64(v))
		}
		if v, ok := cfg.Params["std_dev_times"]; ok {
			stdDevTimes = toFloat64(v)
		}
		return strategy.NewBollinger(cfg.ID, cfg.Symbol, strategy.BollingerConfig{
			Period:      period,
			StdDevTimes: stdDevTimes,
		}), nil

	case "turtle":
		entryPeriod := 20
		exitPeriod := 10
		if v, ok := cfg.Params["entry_period"]; ok {
			entryPeriod = int(toFloat64(v))
		}
		if v, ok := cfg.Params["exit_period"]; ok {
			exitPeriod = int(toFloat64(v))
		}
		return strategy.NewTurtle(cfg.ID, cfg.Symbol, strategy.TurtleConfig{
			EntryPeriod: entryPeriod,
			ExitPeriod:  exitPeriod,
		}), nil

	case "grid":
		gridCount := 10
		gridSpacing := 1.0
		basePrice := int64(0)
		if v, ok := cfg.Params["grid_count"]; ok {
			gridCount = int(toFloat64(v))
		}
		if v, ok := cfg.Params["grid_spacing"]; ok {
			gridSpacing = toFloat64(v)
		}
		if v, ok := cfg.Params["base_price"]; ok {
			basePrice = int64(toFloat64(v))
		}
		return strategy.NewGrid(cfg.ID, cfg.Symbol, strategy.GridConfig{
			GridCount:   gridCount,
			GridSpacing: gridSpacing,
			BasePrice:   basePrice,
		}), nil

	default:
		return nil, fmt.Errorf("unknown strategy type: %s", cfg.Type)
	}
}

func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case float64:
		return val
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return 0
}

func toTypesRiskConfig(cfg RiskConfig) types.RiskConfig {
	return types.RiskConfig{
		MaxSingleOrderValue: cfg.MaxSingleOrderValue,
		MaxPositionValue:    cfg.MaxPositionValue,
		DailyLossLimit:      cfg.DailyLossLimit,
		DisabledSymbols:     cfg.DisabledSymbols,
		CircuitBreakerPct:   cfg.CircuitBreakerPct,
	}
}

func parseDuration(s string, defaultDur time.Duration) time.Duration {
	if s == "" {
		return defaultDur
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultDur
	}
	return d
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file (default: ~/.trading-bot/config.yaml)")
	fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg.Logging)
	slog.SetDefault(logger)

	logger.Info("Starting trading bot",
		"version", Version,
		"build_time", BuildTime,
		"git_commit", GitCommit,
	)

	marketPeriod := parseDuration(cfg.Market.Period, 60*time.Second)
	marketFrequency := parseDuration(cfg.Market.Frequency, time.Second)

	mkt := market.NewMarket(cfg.Market.Symbol, market.FeederConfig{
		Symbol:       cfg.Market.Symbol,
		InitialPrice: cfg.Market.InitialPrice,
		Amplitude:    cfg.Market.Amplitude,
		Period:       marketPeriod,
		Frequency:    marketFrequency,
	})

	orderMgr := order.NewManager()
	posMgr := position.NewManager()
	initialBalance := int64(1000000000000)
	riskMgr := risk.NewManager(toTypesRiskConfig(cfg.Risk), posMgr, initialBalance)
	reportGen := report.NewGenerator(posMgr, orderMgr)

	liveEngine := live.NewEngine(mkt, orderMgr, posMgr, riskMgr, initialBalance)

	for _, sc := range cfg.Strategies {
		if !sc.Enabled {
			continue
		}
		strat, err := createStrategy(sc)
		if err != nil {
			logger.Error("Failed to create strategy", "strategy_id", sc.ID, "error", err)
			continue
		}
		if err := liveEngine.AddStrategy(strat); err != nil {
			logger.Error("Failed to add strategy", "strategy_id", sc.ID, "error", err)
			continue
		}
		logger.Info("Strategy added", "strategy_id", sc.ID, "strategy_type", sc.Type)
	}

	if err := liveEngine.Start(); err != nil {
		logger.Error("Failed to start live engine", "error", err)
		os.Exit(1)
	}
	logger.Info("Live trading engine started")

	var webServer *web.WebServer
	if cfg.Web.Enabled {
		webServer = web.NewServer(cfg.Web.Address, liveEngine, mkt, posMgr, orderMgr, riskMgr, reportGen)
		if err := webServer.Start(); err != nil {
			logger.Error("Failed to start web server", "error", err)
			liveEngine.Stop()
			os.Exit(1)
		}
		logger.Info("Web dashboard started", "address", cfg.Web.Address)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received signal, shutting down", "signal", sig.String())

	if webServer != nil {
		if err := webServer.Stop(); err != nil {
			logger.Error("Error stopping web server", "error", err)
		}
		logger.Info("Web dashboard stopped")
	}

	liveEngine.Stop()
	logger.Info("Live trading engine stopped")
	logger.Info("Shutdown complete")
}

func backtestCmd(args []string) {
	fs := flag.NewFlagSet("backtest", flag.ExitOnError)
	strategyType := fs.String("strategy", "ma_crossover", "Strategy type")
	symbol := fs.String("symbol", "BTCUSDT", "Trading symbol")
	initialBalance := fs.Int64("initial-balance", 1000000000000, "Initial balance (in satoshi)")
	duration := fs.Duration("duration", 24*time.Hour, "Backtest duration")
	output := fs.String("output", "", "Output result file path")
	configPath := fs.String("config", "", "Path to config file (optional)")
	fs.Parse(args)

	logger := setupLogger(LoggingConfig{Level: "info", Format: "text", Output: "stdout"})
	slog.SetDefault(logger)

	logger.Info("Starting backtest",
		"strategy", *strategyType,
		"symbol", *symbol,
		"duration", duration.String(),
	)

	datasetCfg := backtest.DatasetConfig{
		Symbol:       *symbol,
		InitialPrice: 5000000000000,
		Amplitude:    500000000000,
		Period:       60 * time.Second,
		TimeStep:     time.Second,
		Duration:     *duration,
	}

	if *configPath != "" {
		cfg, err := loadConfig(*configPath)
		if err == nil {
			datasetCfg.InitialPrice = cfg.Market.InitialPrice
			datasetCfg.Amplitude = cfg.Market.Amplitude
			datasetCfg.Period = parseDuration(cfg.Market.Period, 60*time.Second)
			datasetCfg.TimeStep = parseDuration(cfg.Market.Frequency, time.Second)
		}
	}

	data := backtest.GenerateMockDataset(datasetCfg)
	logger.Info("Generated dataset", "ticks", len(data))

	stratCfg := StrategyConfig{
		ID:     "bt_001",
		Type:   *strategyType,
		Symbol: *symbol,
		Params: make(map[string]interface{}),
	}

	strat, err := createStrategy(stratCfg)
	if err != nil {
		logger.Error("Failed to create strategy", "error", err)
		os.Exit(1)
	}

	feeRate := 0.001
	slippage := 0.001
	btEngine := backtest.NewEngine(strat, *initialBalance, feeRate, slippage)

	result, err := btEngine.Run(data)
	if err != nil {
		logger.Error("Backtest failed", "error", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("=== Backtest Results ===")
	fmt.Printf("Strategy:        %s\n", *strategyType)
	fmt.Printf("Symbol:          %s\n", *symbol)
	fmt.Printf("Initial Value:   %d (%.2f USDT)\n", result.InitialValue, float64(result.InitialValue)/float64(types.SatoshiPerBTC))
	fmt.Printf("Final Value:     %d (%.2f USDT)\n", result.FinalValue, float64(result.FinalValue)/float64(types.SatoshiPerBTC))
	fmt.Printf("Total Return:    %.2f%%\n", result.TotalReturn*100)
	fmt.Printf("Sharpe Ratio:    %.2f\n", result.SharpeRatio)
	fmt.Printf("Max Drawdown:    %.2f%%\n", result.MaxDrawdown*100)
	fmt.Printf("Win Rate:        %.2f%%\n", result.WinRate*100)
	fmt.Printf("Total Trades:    %d\n", result.TotalTrades)
	fmt.Printf("Winning Trades:  %d\n", result.WinningTrades)
	fmt.Printf("Losing Trades:   %d\n", result.LosingTrades)
	fmt.Println("========================")

	if *output != "" {
		if err := writeBacktestResult(*output, result); err != nil {
			logger.Error("Failed to write output file", "error", err)
			os.Exit(1)
		}
		logger.Info("Results written to file", "output", *output)
	}
}

func writeBacktestResult(path string, result *types.BacktestResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "=== Backtest Results ===\n")
	fmt.Fprintf(f, "Total Return:    %.2f%%\n", result.TotalReturn*100)
	fmt.Fprintf(f, "Sharpe Ratio:    %.2f\n", result.SharpeRatio)
	fmt.Fprintf(f, "Max Drawdown:    %.2f%%\n", result.MaxDrawdown*100)
	fmt.Fprintf(f, "Win Rate:        %.2f%%\n", result.WinRate*100)
	fmt.Fprintf(f, "Total Trades:    %d\n", result.TotalTrades)
	fmt.Fprintf(f, "Initial Value:   %d\n", result.InitialValue)
	fmt.Fprintf(f, "Final Value:     %d\n", result.FinalValue)
	fmt.Fprintf(f, "\n--- Daily PnL ---\n")
	for _, dp := range result.DailyPnL {
		fmt.Fprintf(f, "%s  PnL: %d  Value: %d\n",
			dp.Date.Format("2006-01-02"), dp.PnL, dp.Value)
	}

	return nil
}

func strategiesCmd(args []string) {
	fs := flag.NewFlagSet("strategies", flag.ExitOnError)
	fs.Parse(args)

	fmt.Println("Available Strategies:")
	fmt.Println()

	fmt.Println("1. ma_crossover - Moving Average Crossover")
	fmt.Println("   Parameters:")
	fmt.Println("     fast_period  int   (default: 5)   Fast moving average period")
	fmt.Println("     slow_period  int   (default: 20)  Slow moving average period")
	fmt.Println()

	fmt.Println("2. rsi - Relative Strength Index")
	fmt.Println("   Parameters:")
	fmt.Println("     period       int     (default: 14)   RSI period")
	fmt.Println("     overbought   float   (default: 70)   Overbought threshold")
	fmt.Println("     oversold     float   (default: 30)   Oversold threshold")
	fmt.Println()

	fmt.Println("3. macd - Moving Average Convergence Divergence")
	fmt.Println("   Parameters:")
	fmt.Println("     fast_ema_period   int  (default: 12)  Fast EMA period")
	fmt.Println("     slow_ema_period   int  (default: 26)  Slow EMA period")
	fmt.Println("     signal_period     int  (default: 9)   Signal line period")
	fmt.Println()

	fmt.Println("4. bollinger - Bollinger Bands")
	fmt.Println("   Parameters:")
	fmt.Println("     period         int     (default: 20)  Bollinger band period")
	fmt.Println("     std_dev_times  float   (default: 2)   Standard deviation multiplier")
	fmt.Println()

	fmt.Println("5. turtle - Turtle Trading")
	fmt.Println("   Parameters:")
	fmt.Println("     entry_period  int  (default: 20)  Entry breakout period")
	fmt.Println("     exit_period   int  (default: 10)  Exit breakout period")
	fmt.Println()

	fmt.Println("6. grid - Grid Trading")
	fmt.Println("   Parameters:")
	fmt.Println("     grid_count    int     (default: 10)  Number of grid levels")
	fmt.Println("     grid_spacing  float   (default: 1.0) Grid spacing percentage")
	fmt.Println("     base_price    int64   (default: 0)   Base price for grid")
	fmt.Println()
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to config file (default: ~/.trading-bot/config.yaml)")
	fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg.Logging)
	slog.SetDefault(logger)

	logger.Info("Starting web dashboard only mode")

	marketPeriod := parseDuration(cfg.Market.Period, 60*time.Second)
	marketFrequency := parseDuration(cfg.Market.Frequency, time.Second)

	mkt := market.NewMarket(cfg.Market.Symbol, market.FeederConfig{
		Symbol:       cfg.Market.Symbol,
		InitialPrice: cfg.Market.InitialPrice,
		Amplitude:    cfg.Market.Amplitude,
		Period:       marketPeriod,
		Frequency:    marketFrequency,
	})

	orderMgr := order.NewManager()
	posMgr := position.NewManager()
	initialBalance := int64(1000000000000)
	riskMgr := risk.NewManager(toTypesRiskConfig(cfg.Risk), posMgr, initialBalance)
	reportGen := report.NewGenerator(posMgr, orderMgr)

	liveEngine := live.NewEngine(mkt, orderMgr, posMgr, riskMgr, initialBalance)

	for _, sc := range cfg.Strategies {
		if !sc.Enabled {
			continue
		}
		strat, err := createStrategy(sc)
		if err != nil {
			logger.Error("Failed to create strategy", "strategy_id", sc.ID, "error", err)
			continue
		}
		if err := liveEngine.AddStrategy(strat); err != nil {
			logger.Error("Failed to add strategy", "strategy_id", sc.ID, "error", err)
			continue
		}
	}

	if err := liveEngine.Start(); err != nil {
		logger.Error("Failed to start live engine", "error", err)
		os.Exit(1)
	}

	addr := cfg.Web.Address
	if addr == "" {
		addr = ":8080"
	}

	webServer := web.NewServer(addr, liveEngine, mkt, posMgr, orderMgr, riskMgr, reportGen)
	if err := webServer.Start(); err != nil {
		logger.Error("Failed to start web server", "error", err)
		liveEngine.Stop()
		os.Exit(1)
	}

	logger.Info("Web dashboard started", "address", addr)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received signal, shutting down", "signal", sig.String())

	if err := webServer.Stop(); err != nil {
		logger.Error("Error stopping web server", "error", err)
	}

	liveEngine.Stop()
	logger.Info("Shutdown complete")
}

func versionCmd(args []string) {
	fmt.Printf("Trading Bot v%s\n", Version)
	fmt.Printf("  Build Time: %s\n", BuildTime)
	fmt.Printf("  Git Commit: %s\n", GitCommit)
	fmt.Printf("  Go Version: %s\n", runtimeVersion())
}

func runtimeVersion() string {
	return strings.TrimPrefix(runtime.Version(), "go")
}
