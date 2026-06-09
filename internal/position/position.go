package position

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"trading-bot/pkg/types"
)

var (
	ErrInvalidQuantity = errors.New("invalid quantity")
	ErrInvalidPrice    = errors.New("invalid price")
	ErrInsufficientQty = errors.New("insufficient position quantity")
	ErrOverflow        = errors.New("integer overflow")
)

type Manager struct {
	mu        sync.RWMutex
	positions map[string]*types.Position
}

func NewManager() *Manager {
	return &Manager{
		positions: make(map[string]*types.Position),
	}
}

func (m *Manager) AddPosition(symbol string, qty int64, price int64, side types.OrderSide) (int64, error) {
	if qty <= 0 {
		return 0, ErrInvalidQuantity
	}
	if price <= 0 {
		return 0, ErrInvalidPrice
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	pos, exists := m.positions[symbol]
	if !exists {
		pos = &types.Position{
			Symbol:       symbol,
			Quantity:     0,
			AvgPrice:     0,
			RealizedPnL: 0,
			UpdatedAt:    time.Now(),
		}
		m.positions[symbol] = pos
	}

	var realizedPnL int64

	switch side {
	case types.SideBuy:
		realizedPnL = m.handleBuy(pos, qty, price)
	case types.SideSell:
		var err error
		realizedPnL, err = m.handleSell(pos, qty, price)
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("invalid order side: %v", side)
	}

	pos.UpdatedAt = time.Now()
	return realizedPnL, nil
}

func (m *Manager) handleBuy(pos *types.Position, qty int64, price int64) int64 {
	if pos.Quantity >= 0 {
		totalQty := pos.Quantity + qty
		if totalQty == 0 {
			pos.AvgPrice = 0
		} else {
			bigQtyOld := big.NewInt(pos.Quantity)
			bigPriceOld := big.NewInt(pos.AvgPrice)
			bigQtyNew := big.NewInt(qty)
			bigPriceNew := big.NewInt(price)

			totalCostOld := new(big.Int).Mul(bigQtyOld, bigPriceOld)
			totalCostNew := new(big.Int).Mul(bigQtyNew, bigPriceNew)
			totalCost := new(big.Int).Add(totalCostOld, totalCostNew)
			bigTotalQty := big.NewInt(totalQty)

			avgPrice := new(big.Int).Div(totalCost, bigTotalQty)
			pos.AvgPrice = avgPrice.Int64()
		}
		pos.Quantity = totalQty
		return 0
	}

	coverQty := qty
	if coverQty > -pos.Quantity {
		coverQty = -pos.Quantity
	}

	realizedPnL := types.MulDiv(coverQty, pos.AvgPrice-price, types.SatoshiScale)
	pos.RealizedPnL += realizedPnL

	remainingQty := qty - coverQty
	pos.Quantity += coverQty

	if remainingQty > 0 {
		pos.Quantity = remainingQty
		pos.AvgPrice = price
	}

	return realizedPnL
}

func (m *Manager) handleSell(pos *types.Position, qty int64, price int64) (int64, error) {
	if pos.Quantity > 0 {
		if qty > pos.Quantity {
			return 0, ErrInsufficientQty
		}

		realizedPnL := types.MulDiv(qty, price-pos.AvgPrice, types.SatoshiScale)
		pos.RealizedPnL += realizedPnL
		pos.Quantity -= qty

		if pos.Quantity == 0 {
			pos.AvgPrice = 0
		}

		return realizedPnL, nil
	}

	bigQtyOld := big.NewInt(-pos.Quantity)
	bigPriceOld := big.NewInt(pos.AvgPrice)
	bigQtyNew := big.NewInt(qty)
	bigPriceNew := big.NewInt(price)

	totalCostOld := new(big.Int).Mul(bigQtyOld, bigPriceOld)
	totalCostNew := new(big.Int).Mul(bigQtyNew, bigPriceNew)
	totalCost := new(big.Int).Add(totalCostOld, totalCostNew)
	totalQty := -pos.Quantity + qty
	bigTotalQty := big.NewInt(totalQty)

	avgPrice := new(big.Int).Div(totalCost, bigTotalQty)
	pos.AvgPrice = avgPrice.Int64()
	pos.Quantity -= qty

	return 0, nil
}

func (m *Manager) GetPosition(symbol string) *types.Position {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pos, exists := m.positions[symbol]
	if !exists {
		return nil
	}

	return &types.Position{
		Symbol:       pos.Symbol,
		Quantity:     pos.Quantity,
		AvgPrice:     pos.AvgPrice,
		RealizedPnL: pos.RealizedPnL,
		UpdatedAt:    pos.UpdatedAt,
	}
}

func (m *Manager) GetAllPositions() map[string]*types.Position {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*types.Position, len(m.positions))
	for symbol, pos := range m.positions {
		result[symbol] = &types.Position{
			Symbol:       pos.Symbol,
			Quantity:     pos.Quantity,
			AvgPrice:     pos.AvgPrice,
			RealizedPnL: pos.RealizedPnL,
			UpdatedAt:    pos.UpdatedAt,
		}
	}
	return result
}

func (m *Manager) GetUnrealizedPnL(symbol string, markPrice int64) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pos, exists := m.positions[symbol]
	if !exists || pos.Quantity == 0 {
		return 0
	}

	return types.MulDiv(pos.Quantity, markPrice-pos.AvgPrice, types.SatoshiScale)
}

func (m *Manager) GetTotalExposure(prices map[string]int64) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total int64
	for symbol, pos := range m.positions {
		if pos.Quantity == 0 {
			continue
		}
		price, ok := prices[symbol]
		if !ok {
			continue
		}

		absQty := pos.Quantity
		if absQty < 0 {
			absQty = -absQty
		}

		total += types.CalcValue(absQty, price)
	}
	return total
}

func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.positions = make(map[string]*types.Position)
}
