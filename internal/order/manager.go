package order

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"trading-bot/pkg/types"
)

var (
	ErrOrderNotFound     = errors.New("order not found")
	ErrOrderExists       = errors.New("order already exists")
	ErrInvalidStatus     = errors.New("invalid order status transition")
	ErrInvalidQuantity   = errors.New("invalid quantity")
	ErrInvalidOrderType  = errors.New("invalid order type")
)

type Manager struct {
	mu       sync.RWMutex
	orders   map[string]*types.Order
	counter  uint64
}

func NewManager() *Manager {
	return &Manager{
		orders:  make(map[string]*types.Order),
		counter: 0,
	}
}

func (m *Manager) CreateOrder(order *types.Order) error {
	if order == nil {
		return errors.New("order is nil")
	}
	if order.Quantity <= 0 {
		return ErrInvalidQuantity
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if order.ID == "" {
		m.counter++
		order.ID = fmt.Sprintf("ORD-%d-%d", time.Now().UnixNano(), m.counter)
	} else {
		if _, exists := m.orders[order.ID]; exists {
			return ErrOrderExists
		}
	}

	now := time.Now()
	if order.CreatedAt.IsZero() {
		order.CreatedAt = now
	}
	order.UpdatedAt = now

	if order.Status == 0 {
		order.Status = types.StatusPending
	}

	order.FilledQty = 0

	m.orders[order.ID] = &types.Order{
		ID:         order.ID,
		StrategyID: order.StrategyID,
		Symbol:     order.Symbol,
		Type:       order.Type,
		Side:       order.Side,
		Price:      order.Price,
		Quantity:   order.Quantity,
		FilledQty:  order.FilledQty,
		Status:     order.Status,
		StopPrice:  order.StopPrice,
		CreatedAt:  order.CreatedAt,
		UpdatedAt:  order.UpdatedAt,
		ExchangeTS: order.ExchangeTS,
	}

	return nil
}

func (m *Manager) CancelOrder(orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, exists := m.orders[orderID]
	if !exists {
		return ErrOrderNotFound
	}

	if order.Status != types.StatusOpen {
		return ErrInvalidStatus
	}

	order.Status = types.StatusCancelled
	order.UpdatedAt = time.Now()

	return nil
}

func (m *Manager) GetOrder(orderID string) *types.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	order, exists := m.orders[orderID]
	if !exists {
		return nil
	}

	return copyOrder(order)
}

func (m *Manager) GetOrdersByStrategy(strategyID string) []*types.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*types.Order
	for _, order := range m.orders {
		if order.StrategyID == strategyID {
			result = append(result, copyOrder(order))
		}
	}
	return result
}

func (m *Manager) GetOrdersByStatus(status types.OrderStatus) []*types.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*types.Order
	for _, order := range m.orders {
		if order.Status == status {
			result = append(result, copyOrder(order))
		}
	}
	return result
}

func (m *Manager) GetAllOrders() []*types.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*types.Order, 0, len(m.orders))
	for _, order := range m.orders {
		result = append(result, copyOrder(order))
	}
	return result
}

func (m *Manager) UpdateOrderFill(orderID string, filledQty int64, avgPrice int64, trades []types.Trade) error {
	if filledQty < 0 {
		return ErrInvalidQuantity
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	order, exists := m.orders[orderID]
	if !exists {
		return ErrOrderNotFound
	}

	if order.Status != types.StatusOpen && order.Status != types.StatusPartiallyFilled && order.Status != types.StatusPending {
		return ErrInvalidStatus
	}

	order.FilledQty += filledQty

	if order.FilledQty > order.Quantity {
		order.FilledQty = order.Quantity
	}

	if order.FilledQty == order.Quantity {
		order.Status = types.StatusFilled
	} else if order.FilledQty > 0 {
		order.Status = types.StatusPartiallyFilled
	}

	order.UpdatedAt = time.Now()

	return nil
}

func (m *Manager) UpdateOrderStatus(orderID string, status types.OrderStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, exists := m.orders[orderID]
	if !exists {
		return ErrOrderNotFound
	}

	order.Status = status
	order.UpdatedAt = time.Now()

	return nil
}

func (m *Manager) GetOpenOrders() []*types.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*types.Order
	for _, order := range m.orders {
		if order.Status == types.StatusOpen || order.Status == types.StatusPartiallyFilled || order.Status == types.StatusPending {
			result = append(result, copyOrder(order))
		}
	}
	return result
}

func (m *Manager) CheckStopOrders(currentPrice int64) []*types.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*types.Order
	for _, order := range m.orders {
		if order.Status != types.StatusOpen && order.Status != types.StatusPending {
			continue
		}

		triggered := false

		switch order.Type {
		case types.OrderTypeStopLoss:
			if order.Side == types.SideSell {
				if currentPrice <= order.StopPrice {
					triggered = true
				}
			} else if order.Side == types.SideBuy {
				if currentPrice >= order.StopPrice {
					triggered = true
				}
			}

		case types.OrderTypeTakeProfit:
			if order.Side == types.SideSell {
				if currentPrice >= order.StopPrice {
					triggered = true
				}
			} else if order.Side == types.SideBuy {
				if currentPrice <= order.StopPrice {
					triggered = true
				}
			}
		}

		if triggered {
			result = append(result, copyOrder(order))
		}
	}

	return result
}

func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orders = make(map[string]*types.Order)
	m.counter = 0
}

func copyOrder(order *types.Order) *types.Order {
	if order == nil {
		return nil
	}
	return &types.Order{
		ID:         order.ID,
		StrategyID: order.StrategyID,
		Symbol:     order.Symbol,
		Type:       order.Type,
		Side:       order.Side,
		Price:      order.Price,
		Quantity:   order.Quantity,
		FilledQty:  order.FilledQty,
		Status:     order.Status,
		StopPrice:  order.StopPrice,
		CreatedAt:  order.CreatedAt,
		UpdatedAt:  order.UpdatedAt,
		ExchangeTS: order.ExchangeTS,
	}
}
