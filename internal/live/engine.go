package live

import (
	"errors"
	"sync"
	"time"

	"trading-bot/internal/market"
	"trading-bot/internal/order"
	"trading-bot/internal/position"
	"trading-bot/internal/risk"
	"trading-bot/pkg/types"
)

const (
	defaultFeeRateBps int64 = 10
	maxReconnectAttempts     = 5
	reconnectInterval        = 2 * time.Second
)

var (
	ErrEngineRunning    = errors.New("engine is already running")
	ErrEngineNotRunning = errors.New("engine is not running")
	ErrStrategyExists   = errors.New("strategy already exists")
	ErrStrategyNotFound = errors.New("strategy not found")
	ErrInsufficientBalance = errors.New("insufficient balance")
)

type Engine struct {
	mu           sync.RWMutex
	strategies   map[string]types.Strategy
	market       *market.Market
	orderMgr     *order.Manager
	positionMgr  *position.Manager
	riskMgr      *risk.Manager
	balance      int64
	initialBalance int64
	running      bool
	stopChan     chan struct{}
	wg           sync.WaitGroup
	tradeChan    chan types.Trade
	feeRateBps   int64
}

func NewEngine(mkt *market.Market, orderMgr *order.Manager, posMgr *position.Manager, riskMgr *risk.Manager, initialBalance int64) *Engine {
	return &Engine{
		strategies:     make(map[string]types.Strategy),
		market:         mkt,
		orderMgr:       orderMgr,
		positionMgr:    posMgr,
		riskMgr:        riskMgr,
		balance:        initialBalance,
		initialBalance: initialBalance,
		stopChan:       make(chan struct{}),
		tradeChan:      make(chan types.Trade, 1024),
		feeRateBps:     defaultFeeRateBps,
	}
}

func (e *Engine) AddStrategy(strategy types.Strategy) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if strategy == nil {
		return errors.New("strategy is nil")
	}

	id := strategy.GetID()
	if _, exists := e.strategies[id]; exists {
		return ErrStrategyExists
	}

	e.strategies[id] = strategy
	return nil
}

func (e *Engine) RemoveStrategy(strategyID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	strategy, exists := e.strategies[strategyID]
	if !exists {
		return ErrStrategyNotFound
	}

	strategy.OnStop()
	delete(e.strategies, strategyID)
	return nil
}

func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return ErrEngineRunning
	}

	e.stopChan = make(chan struct{})
	e.running = true

	for _, s := range e.strategies {
		s.OnInit()
	}

	e.wg.Add(1)
	go e.dispatchLoop()

	e.market.StartFeeder()

	return nil
}

func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	close(e.stopChan)
	e.mu.Unlock()

	e.market.StopFeeder()

	e.wg.Wait()

	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, s := range e.strategies {
		s.OnStop()
	}
}

func (e *Engine) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running
}

func (e *Engine) GetBalance() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.balance
}

func (e *Engine) GetStrategies() []types.Strategy {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]types.Strategy, 0, len(e.strategies))
	for _, s := range e.strategies {
		result = append(result, s)
	}
	return result
}

func (e *Engine) TradeChan() <-chan types.Trade {
	return e.tradeChan
}

func (e *Engine) SetFeeRateBps(rate int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.feeRateBps = rate
}

func (e *Engine) GetFeeRateBps() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.feeRateBps
}

func (e *Engine) dispatchLoop() {
	defer e.wg.Done()

	tickChan := e.market.TickChan()

	for {
		select {
		case <-e.stopChan:
			return
		case tick, ok := <-tickChan:
			if !ok {
				e.handleReconnect()
				tickChan = e.market.TickChan()
				continue
			}
			e.processTick(tick)
		}
	}
}

func (e *Engine) handleReconnect() {
	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		select {
		case <-e.stopChan:
			return
		default:
		}

		e.market.StopFeeder()
		time.Sleep(reconnectInterval)
		e.market.StartFeeder()

		tickChan := e.market.TickChan()
		select {
		case <-e.stopChan:
			return
		case _, ok := <-tickChan:
			if ok {
				return
			}
		case <-time.After(reconnectInterval):
		}
	}
}

func (e *Engine) processTick(tick types.Tick) {
	e.mu.RLock()
	strategies := make([]types.Strategy, 0, len(e.strategies))
	for _, s := range e.strategies {
		if s.GetSymbol() == tick.Symbol {
			strategies = append(strategies, s)
		}
	}
	e.mu.RUnlock()

	for _, s := range strategies {
		signal := s.OnTick(tick)
		if signal == types.SignalBuy || signal == types.SignalSell {
			e.executeSignal(s, signal, tick)
		}
	}

	e.checkStopOrders(tick)
}

func (e *Engine) executeSignal(strategy types.Strategy, signal types.SignalType, tick types.Tick) {
	var side types.OrderSide
	if signal == types.SignalBuy {
		side = types.SideBuy
	} else {
		side = types.SideSell
	}

	quantity := e.calculateOrderQuantity(strategy, tick.Price, side)
	if quantity <= 0 {
		return
	}

	order := &types.Order{
		StrategyID: strategy.GetID(),
		Symbol:     tick.Symbol,
		Type:       types.OrderTypeMarket,
		Side:       side,
		Price:      tick.Price,
		Quantity:   quantity,
		ExchangeTS: tick.ExchangeTS,
	}

	if err := e.processOrder(order, tick.Price); err != nil {
		return
	}
}

func (e *Engine) calculateOrderQuantity(strategy types.Strategy, price int64, side types.OrderSide) int64 {
	if side == types.SideBuy {
		e.mu.RLock()
		bal := e.balance
		e.mu.RUnlock()

		if bal <= 0 {
			return 0
		}

		orderValue := bal / 10
		if orderValue <= 0 {
			return 0
		}
		return types.CalcQuantity(orderValue, price)
	}

	pos := e.positionMgr.GetPosition(strategy.GetSymbol())
	if pos == nil || pos.Quantity <= 0 {
		return 0
	}
	return pos.Quantity
}

func (e *Engine) processOrder(order *types.Order, currentPrice int64) error {
	if order.Side == types.SideBuy {
		orderValue := types.CalcValue(order.Quantity, order.Price)
		fee := e.calculateFee(orderValue)
		totalCost := orderValue + fee

		e.mu.RLock()
		bal := e.balance
		e.mu.RUnlock()

		if totalCost > bal {
			return ErrInsufficientBalance
		}
	} else {
		pos := e.positionMgr.GetPosition(order.Symbol)
		if pos == nil || pos.Quantity < order.Quantity {
			return position.ErrInsufficientQty
		}
	}

	if err := e.riskMgr.ValidateOrder(order, currentPrice); err != nil {
		return err
	}

	if err := e.orderMgr.CreateOrder(order); err != nil {
		return err
	}

	if err := e.orderMgr.UpdateOrderStatus(order.ID, types.StatusOpen); err != nil {
		return err
	}

	trades, _ := e.market.ProcessOrder(order)
	if len(trades) == 0 {
		return nil
	}

	e.populateTradeFees(trades)

	totalFilledQty := int64(0)
	totalFee := int64(0)

	for _, trade := range trades {
		totalFilledQty += trade.Quantity
		totalFee += trade.Fee

		select {
		case e.tradeChan <- trade:
		default:
		}
	}

	avgPrice := int64(0)
	if totalFilledQty > 0 {
		totalValue := int64(0)
		for _, t := range trades {
			totalValue += types.CalcValue(t.Quantity, t.Price)
		}
		avgPrice = totalValue * types.SatoshiScale / totalFilledQty
	}

	if err := e.orderMgr.UpdateOrderFill(order.ID, totalFilledQty, avgPrice, trades); err != nil {
		return err
	}

	realizedPnL, err := e.positionMgr.AddPosition(order.Symbol, totalFilledQty, avgPrice, order.Side)
	if err != nil {
		return err
	}

	e.updateBalance(order.Side, totalFilledQty, avgPrice, totalFee)

	e.riskMgr.RecordPnL(realizedPnL - totalFee)

	return nil
}

func (e *Engine) populateTradeFees(trades []types.Trade) {
	rate := e.GetFeeRateBps()
	for i := range trades {
		tradeValue := types.CalcValue(trades[i].Quantity, trades[i].Price)
		trades[i].Fee = tradeValue * rate / 10000
	}
}

func (e *Engine) calculateFee(tradeValue int64) int64 {
	e.mu.RLock()
	rate := e.feeRateBps
	e.mu.RUnlock()

	return tradeValue * rate / 10000
}

func (e *Engine) updateBalance(side types.OrderSide, qty int64, price int64, fee int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tradeValue := types.CalcValue(qty, price)

	if side == types.SideBuy {
		e.balance -= tradeValue + fee
	} else {
		e.balance += tradeValue - fee
	}
}

func (e *Engine) checkStopOrders(tick types.Tick) {
	stopOrders := e.orderMgr.CheckStopOrders(tick.Price)

	for _, ord := range stopOrders {
		remainingQty := ord.Quantity - ord.FilledQty
		if remainingQty <= 0 {
			continue
		}

		e.market.CancelOrder(ord.ID)
		e.orderMgr.CancelOrder(ord.ID)

		order := &types.Order{
			StrategyID: ord.StrategyID,
			Symbol:     ord.Symbol,
			Type:       types.OrderTypeMarket,
			Side:       ord.Side,
			Price:      tick.Price,
			Quantity:   remainingQty,
			ExchangeTS: tick.ExchangeTS,
		}
		_ = e.processOrder(order, tick.Price)
	}
}

func (e *Engine) GetEquity(currentPrice int64) int64 {
	e.mu.RLock()
	bal := e.balance
	e.mu.RUnlock()

	positions := e.positionMgr.GetAllPositions()
	var positionValue int64
	for _, pos := range positions {
		positionValue += types.CalcValue(pos.Quantity, currentPrice)
	}

	return bal + positionValue
}

func (e *Engine) Reset(initialBalance int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.balance = initialBalance
	e.initialBalance = initialBalance
	e.strategies = make(map[string]types.Strategy)
	e.running = false

	select {
	case <-e.stopChan:
	default:
		close(e.stopChan)
	}
	e.stopChan = make(chan struct{})

	e.tradeChan = make(chan types.Trade, 1024)
}
