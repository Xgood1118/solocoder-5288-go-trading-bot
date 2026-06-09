package market

import (
	"testing"
	"time"

	"trading-bot/pkg/types"
)

func TestMockMarketMaker(t *testing.T) {
	symbol := "BTCUSDT"
	feederCfg := FeederConfig{
		Symbol:      symbol,
		InitialPrice: 50000_00000000,
		Amplitude:    5000_00000000,
		Period:       60 * time.Second,
		Frequency:    100 * time.Millisecond,
		NoiseStdDev:  100_00000000,
	}

	mkt := NewMarket(symbol, feederCfg)

	bestBid, _ := mkt.GetBestBid()
	bestAsk, _ := mkt.GetBestAsk()
	if bestBid != 0 || bestAsk != 0 {
		t.Error("order book should be empty before starting feeder")
	}

	mkt.StartFeeder()
	defer mkt.StopFeeder()

	time.Sleep(500 * time.Millisecond)

	bestBid, bidQty := mkt.GetBestBid()
	bestAsk, askQty := mkt.GetBestAsk()

	t.Logf("Best Bid: %d, Qty: %d", bestBid, bidQty)
	t.Logf("Best Ask: %d, Qty: %d", bestAsk, askQty)

	if bestBid == 0 {
		t.Error("best bid should not be zero after starting feeder")
	}
	if bestAsk == 0 {
		t.Error("best ask should not be zero after starting feeder")
	}
	if bestBid >= bestAsk {
		t.Errorf("best bid (%d) should be less than best ask (%d)", bestBid, bestAsk)
	}
	if bidQty <= 0 {
		t.Error("bid quantity should be positive")
	}
	if askQty <= 0 {
		t.Error("ask quantity should be positive")
	}

	depth := mkt.GetDepth(10)
	if len(depth.Bids) == 0 || len(depth.Asks) == 0 {
		t.Error("order book depth should have bids and asks")
	}
	t.Logf("Depth bids: %d levels, asks: %d levels", len(depth.Bids), len(depth.Asks))
}

func TestMockMarketMaker_MarketOrderFill(t *testing.T) {
	symbol := "BTCUSDT"
	feederCfg := FeederConfig{
		Symbol:      symbol,
		InitialPrice: 50000_00000000,
		Amplitude:    5000_00000000,
		Period:       60 * time.Second,
		Frequency:    100 * time.Millisecond,
		NoiseStdDev:  100_00000000,
	}

	mkt := NewMarket(symbol, feederCfg)
	mkt.StartFeeder()
	defer mkt.StopFeeder()

	time.Sleep(200 * time.Millisecond)

	buyOrder := &types.Order{
		ID:       "test-buy-1",
		Symbol:   symbol,
		Type:     types.OrderTypeMarket,
		Side:     types.SideBuy,
		Price:    0,
		Quantity: 5000_0000,
		Status:   types.StatusPending,
	}

	trades, updatedOrder := mkt.ProcessOrder(buyOrder)

	t.Logf("Buy trades: %d", len(trades))
	t.Logf("Order status: %v", updatedOrder.Status)
	t.Logf("Order filled qty: %d", updatedOrder.FilledQty)

	if len(trades) == 0 {
		t.Error("market buy order should fill against mock MM asks")
	}

	if updatedOrder.FilledQty <= 0 {
		t.Error("order should have some filled quantity")
	}

	sellOrder := &types.Order{
		ID:       "test-sell-1",
		Symbol:   symbol,
		Type:     types.OrderTypeMarket,
		Side:     types.SideSell,
		Price:    0,
		Quantity: 5000_0000,
		Status:   types.StatusPending,
	}

	trades2, updatedOrder2 := mkt.ProcessOrder(sellOrder)

	t.Logf("Sell trades: %d", len(trades2))
	t.Logf("Sell order status: %v", updatedOrder2.Status)

	if len(trades2) == 0 {
		t.Error("market sell order should fill against mock MM bids")
	}
}

func TestTickChan(t *testing.T) {
	symbol := "BTCUSDT"
	feederCfg := FeederConfig{
		Symbol:      symbol,
		InitialPrice: 50000_00000000,
		Amplitude:    5000_00000000,
		Period:       60 * time.Second,
		Frequency:    50 * time.Millisecond,
		NoiseStdDev:  100_00000000,
	}

	mkt := NewMarket(symbol, feederCfg)
	mkt.StartFeeder()
	defer mkt.StopFeeder()

	tickChan := mkt.TickChan()

	tickCount := 0
	timeout := time.After(500 * time.Millisecond)

	for tickCount < 5 {
		select {
		case tick, ok := <-tickChan:
			if !ok {
				t.Fatal("tick channel closed")
			}
			if tick.Price <= 0 {
				t.Error("tick price should be positive")
			}
			if tick.Symbol != symbol {
				t.Errorf("tick symbol mismatch: got %s, want %s", tick.Symbol, symbol)
			}
			tickCount++
		case <-timeout:
			break
		}
	}

	if tickCount == 0 {
		t.Error("should receive at least some ticks")
	}
	t.Logf("Received %d ticks", tickCount)
}
