package risk

import (
	"errors"
	"fmt"
	"sync"

	"trading-bot/internal/position"
	"trading-bot/pkg/types"
)

var (
	ErrSymbolDisabled    = errors.New("symbol is disabled")
	ErrCircuitBroken     = errors.New("circuit breaker is triggered")
	ErrOrderValueTooLarge = errors.New("order value exceeds max single order value")
	ErrPositionLimit      = errors.New("position value exceeds max position value")
	ErrInvalidConfig      = errors.New("invalid risk config")
	ErrOverflow           = errors.New("integer overflow")
)

type Manager struct {
	mu             sync.RWMutex
	config         types.RiskConfig
	positionMgr    *position.Manager
	initialBalance int64
	dailyPnL       int64
	circuitBroken  bool
	disabledMap    map[string]struct{}
}

func NewManager(config types.RiskConfig, positionMgr *position.Manager, initialBalance int64) *Manager {
	m := &Manager{
		config:         config,
		positionMgr:    positionMgr,
		initialBalance: initialBalance,
		dailyPnL:       0,
		circuitBroken:  false,
		disabledMap:    make(map[string]struct{}),
	}
	for _, symbol := range config.DisabledSymbols {
		m.disabledMap[symbol] = struct{}{}
	}
	return m
}

func (m *Manager) ValidateOrder(order *types.Order, currentPrice int64) error {
	if order == nil {
		return errors.New("order is nil")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, disabled := m.disabledMap[order.Symbol]; disabled {
		return ErrSymbolDisabled
	}

	if m.circuitBroken {
		return ErrCircuitBroken
	}

	price := order.Price
	if price <= 0 {
		price = currentPrice
	}

	orderValue := types.CalcValue(order.Quantity, price)

	if m.config.MaxSingleOrderValue > 0 && orderValue > m.config.MaxSingleOrderValue {
		return fmt.Errorf("%w: order value %d, max %d", ErrOrderValueTooLarge, orderValue, m.config.MaxSingleOrderValue)
	}

	if order.Side == types.SideBuy && m.config.MaxPositionValue > 0 {
		pos := m.positionMgr.GetPosition(order.Symbol)
		var currentQty int64
		if pos != nil {
			currentQty = pos.Quantity
		}
		if currentQty < 0 {
			currentQty = 0
		}

		newQty := currentQty + order.Quantity
		newPosValue := types.CalcValue(newQty, price)

		if newPosValue > m.config.MaxPositionValue {
			return fmt.Errorf("%w: new position value %d, max %d", ErrPositionLimit, newPosValue, m.config.MaxPositionValue)
		}
	}

	return nil
}

func (m *Manager) CheckCircuitBreaker() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.circuitBroken
}

func (m *Manager) RecordPnL(pnl int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.dailyPnL += pnl

	if m.initialBalance > 0 && m.config.CircuitBreakerPct > 0 {
		lossThreshold := int64(-float64(m.initialBalance) * m.config.CircuitBreakerPct)
		if m.dailyPnL < lossThreshold {
			m.circuitBroken = true
		}
	}

	if m.config.DailyLossLimit > 0 && m.dailyPnL < -m.config.DailyLossLimit {
		m.circuitBroken = true
	}
}

func (m *Manager) GetDailyPnL() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dailyPnL
}

func (m *Manager) IsCircuitBroken() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.circuitBroken
}

func (m *Manager) ResetDaily() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dailyPnL = 0
	m.circuitBroken = false
}

func (m *Manager) AddDisabledSymbol(symbol string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disabledMap[symbol] = struct{}{}
	m.updateDisabledSymbols()
}

func (m *Manager) RemoveDisabledSymbol(symbol string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.disabledMap, symbol)
	m.updateDisabledSymbols()
}

func (m *Manager) updateDisabledSymbols() {
	symbols := make([]string, 0, len(m.disabledMap))
	for s := range m.disabledMap {
		symbols = append(symbols, s)
	}
	m.config.DisabledSymbols = symbols
}

func (m *Manager) UpdateConfig(config types.RiskConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config
	m.disabledMap = make(map[string]struct{})
	for _, symbol := range config.DisabledSymbols {
		m.disabledMap[symbol] = struct{}{}
	}
}

func (m *Manager) GetTotalExposure(prices map[string]int64) int64 {
	return m.positionMgr.GetTotalExposure(prices)
}

func mul64(a, b int64) (int64, bool) {
	result := a * b
	if a == 0 || b == 0 {
		return 0, true
	}
	if a == -1 && b == -9223372036854775808 {
		return 0, false
	}
	if b == -1 && a == -9223372036854775808 {
		return 0, false
	}
	if result/b != a {
		return 0, false
	}
	return result, true
}
