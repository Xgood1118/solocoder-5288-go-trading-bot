package market

import (
	"trading-bot/pkg/types"
)

type Market struct {
	symbol     string
	orderBook  *OrderBook
	matching   *MatchingEngine
	feeder     *MockFeeder
}

func NewMarket(symbol string, feederConfig FeederConfig) *Market {
	orderBook := NewOrderBook(symbol)
	matching := NewMatchingEngine(orderBook)
	feeder := NewMockFeeder(feederConfig)

	return &Market{
		symbol:    symbol,
		orderBook: orderBook,
		matching:  matching,
		feeder:    feeder,
	}
}

func (m *Market) Symbol() string {
	return m.symbol
}

func (m *Market) OrderBook() *OrderBook {
	return m.orderBook
}

func (m *Market) MatchingEngine() *MatchingEngine {
	return m.matching
}

func (m *Market) Feeder() *MockFeeder {
	return m.feeder
}

func (m *Market) StartFeeder() {
	m.feeder.Start()
}

func (m *Market) StopFeeder() {
	m.feeder.Stop()
}

func (m *Market) TickChan() <-chan types.Tick {
	return m.feeder.TickChan()
}

func (m *Market) ProcessOrder(order *types.Order) ([]types.Trade, *types.Order) {
	return m.matching.ProcessOrder(order)
}

func (m *Market) CancelOrder(orderID string) bool {
	return m.matching.CancelOrder(orderID)
}

func (m *Market) GetBestBid() (int64, int64) {
	return m.orderBook.GetBestBid()
}

func (m *Market) GetBestAsk() (int64, int64) {
	return m.orderBook.GetBestAsk()
}

func (m *Market) GetDepth(levels int) types.OrderBook {
	return m.orderBook.GetDepth(levels)
}
