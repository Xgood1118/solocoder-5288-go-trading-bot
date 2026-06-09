package backtest

import (
	"testing"
	"time"

	"trading-bot/internal/strategy"
	"trading-bot/pkg/types"
)

func TestBacktestEngine_Basic(t *testing.T) {
	symbol := "BTCUSDT"
	initialBalance := int64(10000_00000000)

	cfg := strategy.MACrossoverConfig{
		FastPeriod: 5,
		SlowPeriod: 20,
	}
	strategyInst := strategy.NewMACrossover("test-ma", symbol, cfg)

	eng := NewEngine(strategyInst, initialBalance, 0.001, 0.001)

	datasetCfg := DatasetConfig{
		Symbol:      symbol,
		InitialPrice: 50000_00000000,
		Amplitude:    5000_00000000,
		Period:       7 * 24 * time.Hour,
		NoiseStdDev:  100_00000000,
		TimeStep:     1 * time.Hour,
		Duration:     30 * 24 * time.Hour,
	}
	data := GenerateMockDataset(datasetCfg)

	if len(data) == 0 {
		t.Fatal("no data generated")
	}

	result, err := eng.Run(data)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result == nil {
		t.Fatal("result is nil")
	}

	t.Logf("Total Trades: %d", result.TotalTrades)
	t.Logf("Win Rate: %.2f%%", result.WinRate*100)
	t.Logf("Total Return: %.2f%%", result.TotalReturn*100)
	t.Logf("Max Drawdown: %.2f%%", result.MaxDrawdown*100)
	t.Logf("Sharpe Ratio: %.4f", result.SharpeRatio)
	t.Logf("Final Value: %d", result.FinalValue)

	if result.TotalTrades == 0 {
		t.Error("expected at least 1 trade, got 0")
	}

	if result.FinalValue <= 0 {
		t.Error("final value should be positive")
	}

	if result.InitialValue != initialBalance {
		t.Errorf("initial value mismatch: got %d, want %d", result.InitialValue, initialBalance)
	}
}

func TestBacktestEngine_ExecuteBuy(t *testing.T) {
	symbol := "BTCUSDT"
	initialBalance := int64(10000_00000000)

	cfg := strategy.MACrossoverConfig{
		FastPeriod: 5,
		SlowPeriod: 20,
	}
	strategyInst := strategy.NewMACrossover("test-ma", symbol, cfg)

	eng := NewEngine(strategyInst, initialBalance, 0.001, 0.001)

	price := int64(50000_00000000)
	tick := types.Tick{
		Symbol:    symbol,
		Price:     price,
		Timestamp: time.Now(),
	}

	eng.executeBuy(tick)

	if len(eng.trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(eng.trades))
	}

	trade := eng.trades[0]
	if trade.Quantity <= 0 {
		t.Error("trade quantity should be positive")
	}
	if trade.Fee <= 0 {
		t.Error("trade fee should be positive")
	}

	pos := eng.positionMgr.GetPosition(symbol)
	if pos == nil {
		t.Fatal("position should exist")
	}
	if pos.Quantity != trade.Quantity {
		t.Errorf("position quantity mismatch: got %d, want %d", pos.Quantity, trade.Quantity)
	}

	if eng.balance >= initialBalance {
		t.Error("balance should decrease after buy")
	}
}

func TestBacktestEngine_ExecuteSell(t *testing.T) {
	symbol := "BTCUSDT"
	initialBalance := int64(10000_00000000)

	cfg := strategy.MACrossoverConfig{
		FastPeriod: 5,
		SlowPeriod: 20,
	}
	strategyInst := strategy.NewMACrossover("test-ma", symbol, cfg)

	eng := NewEngine(strategyInst, initialBalance, 0.001, 0.001)

	buyPrice := int64(50000_00000000)
	buyTick := types.Tick{
		Symbol:    symbol,
		Price:     buyPrice,
		Timestamp: time.Now(),
	}
	eng.executeBuy(buyTick)

	if len(eng.trades) != 1 {
		t.Fatalf("expected 1 trade after buy, got %d", len(eng.trades))
	}

	sellPrice := int64(55000_00000000)
	sellTick := types.Tick{
		Symbol:    symbol,
		Price:     sellPrice,
		Timestamp: time.Now().Add(time.Hour),
	}
	eng.executeSell(sellTick)

	if len(eng.trades) != 2 {
		t.Fatalf("expected 2 trades after sell, got %d", len(eng.trades))
	}

	pos := eng.positionMgr.GetPosition(symbol)
	if pos == nil {
		t.Fatal("position should exist")
	}
	if pos.Quantity != 0 {
		t.Errorf("position should be zero after full sell, got %d", pos.Quantity)
	}
	if pos.RealizedPnL <= 0 {
		t.Errorf("should have positive realized PnL, got %d", pos.RealizedPnL)
	}

	t.Logf("Realized PnL: %d", pos.RealizedPnL)
	t.Logf("Final balance: %d", eng.balance)
}

func TestCalcValue(t *testing.T) {
	qty := int64(1_00000000)
	price := int64(50000_00000000)

	value := types.CalcValue(qty, price)
	expected := int64(50000_00000000)

	if value != expected {
		t.Errorf("CalcValue: expected %d, got %d", expected, value)
	}
}

func TestCalcQuantity(t *testing.T) {
	value := int64(50000_00000000)
	price := int64(50000_00000000)

	qty := types.CalcQuantity(value, price)
	expected := int64(1_00000000)

	if qty != expected {
		t.Errorf("CalcQuantity: expected %d, got %d", expected, qty)
	}
}

func TestMulDiv(t *testing.T) {
	result := types.MulDiv(100, 200, 10)
	expected := int64(2000)
	if result != expected {
		t.Errorf("MulDiv: expected %d, got %d", expected, result)
	}

	result2 := types.MulDiv(1_00000000, 50000_00000000, 1_00000000)
	expected2 := int64(50000_00000000)
	if result2 != expected2 {
		t.Errorf("MulDiv (large numbers): expected %d, got %d", expected2, result2)
	}
}
