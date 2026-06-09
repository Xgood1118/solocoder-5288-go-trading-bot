package live

import (
	"testing"
	"time"

	"trading-bot/internal/market"
	"trading-bot/internal/order"
	"trading-bot/internal/position"
	"trading-bot/internal/risk"
	"trading-bot/internal/strategy"
	"trading-bot/pkg/types"
)

func TestLiveEngine_BasicFlow(t *testing.T) {
	symbol := "BTCUSDT"
	initialBalance := int64(10000_00000000)

	feederCfg := market.FeederConfig{
		Symbol:      symbol,
		InitialPrice: 50000_00000000,
		Amplitude:    2000_00000000,
		Period:       10 * time.Second,
		Frequency:    50 * time.Millisecond,
		NoiseStdDev:  100_00000000,
	}

	mkt := market.NewMarket(symbol, feederCfg)
	orderMgr := order.NewManager()
	posMgr := position.NewManager()
	riskMgr := risk.NewManager(types.RiskConfig{}, posMgr, initialBalance)

	eng := NewEngine(mkt, orderMgr, posMgr, riskMgr, initialBalance)

	cfg := strategy.MACrossoverConfig{
		FastPeriod: 3,
		SlowPeriod: 10,
	}
	strat := strategy.NewMACrossover("test-ma", symbol, cfg)
	if err := eng.AddStrategy(strat); err != nil {
		t.Fatalf("AddStrategy failed: %v", err)
	}

	if err := eng.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer eng.Stop()

	time.Sleep(2 * time.Second)

	balance := eng.GetBalance()
	t.Logf("Balance: %d", balance)

	positions := posMgr.GetAllPositions()
	t.Logf("Number of positions: %d", len(positions))
	for _, pos := range positions {
		t.Logf("Position: %s, Qty: %d, AvgPrice: %d, RealizedPnL: %d",
			pos.Symbol, pos.Quantity, pos.AvgPrice, pos.RealizedPnL)
	}

	orders := orderMgr.GetAllOrders()
	t.Logf("Number of orders: %d", len(orders))

	bestBid, _ := mkt.GetBestBid()
	bestAsk, _ := mkt.GetBestAsk()
	t.Logf("Best Bid: %d, Best Ask: %d", bestBid, bestAsk)

	if bestBid == 0 || bestAsk == 0 {
		t.Error("order book should have bid and ask prices")
	}

	if eng.IsRunning() != true {
		t.Error("engine should be running")
	}
}

func TestLiveEngine_CalculateOrderQuantity(t *testing.T) {
	symbol := "BTCUSDT"
	initialBalance := int64(10000_00000000)

	feederCfg := market.FeederConfig{
		Symbol:      symbol,
		InitialPrice: 50000_00000000,
		Amplitude:    5000_00000000,
		Period:       60 * time.Second,
		Frequency:    time.Second,
	}

	mkt := market.NewMarket(symbol, feederCfg)
	orderMgr := order.NewManager()
	posMgr := position.NewManager()
	riskMgr := risk.NewManager(types.RiskConfig{}, posMgr, initialBalance)

	eng := NewEngine(mkt, orderMgr, posMgr, riskMgr, initialBalance)

	cfg := strategy.MACrossoverConfig{
		FastPeriod: 5,
		SlowPeriod: 20,
	}
	strat := strategy.NewMACrossover("test-ma", symbol, cfg)
	eng.AddStrategy(strat)

	price := int64(50000_00000000)
	qty := eng.calculateOrderQuantity(strat, price, types.SideBuy)
	t.Logf("Buy quantity: %d (%.6f BTC)", qty, float64(qty)/float64(types.SatoshiPerBTC))

	if qty <= 0 {
		t.Error("buy quantity should be positive")
	}

	expectedValue := initialBalance / 10
	actualValue := types.CalcValue(qty, price)
	t.Logf("Expected order value: ~%d, actual: %d", expectedValue, actualValue)

	if actualValue <= 0 {
		t.Error("order value should be positive")
	}
}
