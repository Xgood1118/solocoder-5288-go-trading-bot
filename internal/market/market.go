package market

import (
	"fmt"
	"sync"
	"time"

	"trading-bot/pkg/types"
)

const (
	mockMMStrategyID = "mock-market-maker"
	mockMMSpreadBps  = 10
	mockMMLevels     = 5
	mockMMQtyPerLevel = 1_00000000
)

type Market struct {
	symbol      string
	orderBook   *OrderBook
	matching    *MatchingEngine
	feeder      *MockFeeder
	outTickChan chan types.Tick
	mockMMOrderIDs []string
	mockMMmu    sync.Mutex
	running     bool
	stopChan    chan struct{}
	wg          sync.WaitGroup
}

func NewMarket(symbol string, feederConfig FeederConfig) *Market {
	orderBook := NewOrderBook(symbol)
	matching := NewMatchingEngine(orderBook)
	feeder := NewMockFeeder(feederConfig)

	return &Market{
		symbol:       symbol,
		orderBook:    orderBook,
		matching:     matching,
		feeder:       feeder,
		outTickChan:  make(chan types.Tick, 1024),
		stopChan:     make(chan struct{}),
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
	m.mockMMmu.Lock()
	if m.running {
		m.mockMMmu.Unlock()
		return
	}
	m.running = true
	m.stopChan = make(chan struct{})
	m.mockMMmu.Unlock()

	m.feeder.Start()

	m.wg.Add(1)
	go m.tickProcessor()
}

func (m *Market) StopFeeder() {
	m.mockMMmu.Lock()
	if !m.running {
		m.mockMMmu.Unlock()
		return
	}
	m.running = false
	close(m.stopChan)
	m.mockMMmu.Unlock()

	m.feeder.Stop()
	m.wg.Wait()

	m.clearMockMMOrders()
}

func (m *Market) TickChan() <-chan types.Tick {
	return m.outTickChan
}

func (m *Market) tickProcessor() {
	defer m.wg.Done()

	tickChan := m.feeder.TickChan()

	for {
		select {
		case <-m.stopChan:
			return
		case tick, ok := <-tickChan:
			if !ok {
				return
			}
			m.updateMockMM(tick.Price)

			select {
			case m.outTickChan <- tick:
			default:
			}
		}
	}
}

func (m *Market) updateMockMM(price int64) {
	m.mockMMmu.Lock()
	defer m.mockMMmu.Unlock()

	for _, id := range m.mockMMOrderIDs {
		m.orderBook.CancelOrder(id)
	}
	m.mockMMOrderIDs = nil

	for i := 1; i <= mockMMLevels; i++ {
		bidPrice := price * int64(10000-int64(i)*mockMMSpreadBps) / 10000
		bidOrder := &types.Order{
			ID:         fmt.Sprintf("mock-mm-bid-%d-%d", i, time.Now().UnixNano()),
			StrategyID: mockMMStrategyID,
			Symbol:     m.symbol,
			Type:       types.OrderTypeLimit,
			Side:       types.SideBuy,
			Price:      bidPrice,
			Quantity:   mockMMQtyPerLevel,
			Status:     types.StatusOpen,
		}
		m.orderBook.AddOrder(bidOrder)
		m.mockMMOrderIDs = append(m.mockMMOrderIDs, bidOrder.ID)

		askPrice := price * int64(10000+int64(i)*mockMMSpreadBps) / 10000
		askOrder := &types.Order{
			ID:         fmt.Sprintf("mock-mm-ask-%d-%d", i, time.Now().UnixNano()),
			StrategyID: mockMMStrategyID,
			Symbol:     m.symbol,
			Type:       types.OrderTypeLimit,
			Side:       types.SideSell,
			Price:      askPrice,
			Quantity:   mockMMQtyPerLevel,
			Status:     types.StatusOpen,
		}
		m.orderBook.AddOrder(askOrder)
		m.mockMMOrderIDs = append(m.mockMMOrderIDs, askOrder.ID)
	}
}

func (m *Market) clearMockMMOrders() {
	m.mockMMmu.Lock()
	defer m.mockMMmu.Unlock()

	for _, id := range m.mockMMOrderIDs {
		m.orderBook.CancelOrder(id)
	}
	m.mockMMOrderIDs = nil
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
