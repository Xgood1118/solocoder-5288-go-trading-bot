package backtest

import (
	"errors"
	"math"
	"sort"
	"time"

	"trading-bot/internal/position"
	"trading-bot/pkg/types"
)

var (
	ErrEmptyData     = errors.New("empty tick data")
	ErrNilStrategy   = errors.New("nil strategy")
	ErrInvalidBalance = errors.New("invalid initial balance")
)

type Engine struct {
	positionMgr   *position.Manager
	initialBalance int64
	balance       int64
	feeRate       float64
	slippage      float64
	trades        []types.Trade
	dailyPnL      []types.DailyPnL
	strategy      types.Strategy
	tradeCounter  int
}

func NewEngine(strategy types.Strategy, initialBalance int64, feeRate float64, slippage float64) *Engine {
	if feeRate < 0 {
		feeRate = 0.001
	}
	if slippage < 0 {
		slippage = 0.001
	}

	return &Engine{
		positionMgr:   position.NewManager(),
		initialBalance: initialBalance,
		balance:       initialBalance,
		feeRate:       feeRate,
		slippage:      slippage,
		trades:        make([]types.Trade, 0),
		dailyPnL:      make([]types.DailyPnL, 0),
		strategy:      strategy,
		tradeCounter:  0,
	}
}

func (e *Engine) Run(data []types.Tick) (*types.BacktestResult, error) {
	if e.strategy == nil {
		return nil, ErrNilStrategy
	}
	if len(data) == 0 {
		return nil, ErrEmptyData
	}
	if e.initialBalance <= 0 {
		return nil, ErrInvalidBalance
	}

	ticks := make([]types.Tick, len(data))
	copy(ticks, data)
	sort.Slice(ticks, func(i, j int) bool {
		return ticks[i].Timestamp.Before(ticks[j].Timestamp)
	})

	e.Reset()
	e.strategy.OnInit()
	defer e.strategy.OnStop()

	var currentDate time.Time
	var dayStartValue int64 = e.initialBalance

	for i := 0; i < len(ticks); i++ {
		tick := ticks[i]

		signal := e.strategy.OnTick(tick)

		switch signal {
		case types.SignalBuy:
			e.executeBuy(tick)
		case types.SignalSell:
			e.executeSell(tick)
		}

		tickDate := tick.Timestamp.Truncate(24 * time.Hour)
		if !tickDate.Equal(currentDate) {
			if !currentDate.IsZero() {
				currentValue := e.calculateTotalValue(tick.Price)
				dailyPnL := currentValue - dayStartValue
				e.dailyPnL = append(e.dailyPnL, types.DailyPnL{
					Date:  currentDate,
					PnL:   dailyPnL,
					Value: currentValue,
				})
				dayStartValue = currentValue
			} else {
				dayStartValue = e.initialBalance
			}
			currentDate = tickDate
		}
	}

	if !currentDate.IsZero() && len(ticks) > 0 {
		lastPrice := ticks[len(ticks)-1].Price
		currentValue := e.calculateTotalValue(lastPrice)
		dailyPnL := currentValue - dayStartValue
		e.dailyPnL = append(e.dailyPnL, types.DailyPnL{
			Date:  currentDate,
			PnL:   dailyPnL,
			Value: currentValue,
		})
	}

	return e.calculateResult(ticks), nil
}

func (e *Engine) Reset() {
	e.positionMgr.Reset()
	e.balance = e.initialBalance
	e.trades = make([]types.Trade, 0)
	e.dailyPnL = make([]types.DailyPnL, 0)
	e.tradeCounter = 0
}

func (e *Engine) executeBuy(tick types.Tick) {
	if e.balance <= 0 {
		return
	}

	buyPrice := int64(float64(tick.Price) * (1 + e.slippage))
	if buyPrice <= 0 {
		return
	}

	maxNotional := int64(float64(e.balance) / (1 + e.feeRate))
	if maxNotional <= 0 {
		return
	}

	quantity := types.CalcQuantity(maxNotional, buyPrice)
	if quantity <= 0 {
		return
	}

	notional := types.CalcValue(quantity, buyPrice)
	fee := int64(float64(notional) * e.feeRate)
	totalCost := notional + fee

	if totalCost > e.balance {
		return
	}

	e.balance -= totalCost

	_, err := e.positionMgr.AddPosition(tick.Symbol, quantity, buyPrice, types.SideBuy)
	if err != nil {
		e.balance += totalCost
		return
	}

	e.tradeCounter++
	trade := types.Trade{
		ID:         generateTradeID(e.tradeCounter),
		OrderID:    generateOrderID(e.tradeCounter),
		Symbol:     tick.Symbol,
		Side:       types.SideBuy,
		Price:      buyPrice,
		Quantity:   quantity,
		Fee:        fee,
		Timestamp:  tick.Timestamp,
		ExchangeTS: tick.ExchangeTS,
	}
	e.trades = append(e.trades, trade)
}

func (e *Engine) executeSell(tick types.Tick) {
	pos := e.positionMgr.GetPosition(tick.Symbol)
	if pos == nil || pos.Quantity <= 0 {
		return
	}

	quantity := pos.Quantity
	sellPrice := int64(float64(tick.Price) * (1 - e.slippage))
	if sellPrice <= 0 {
		return
	}

	notional := types.CalcValue(quantity, sellPrice)
	fee := int64(float64(notional) * e.feeRate)

	e.positionMgr.AddPosition(tick.Symbol, quantity, sellPrice, types.SideSell)

	e.balance += notional - fee

	e.tradeCounter++
	trade := types.Trade{
		ID:         generateTradeID(e.tradeCounter),
		OrderID:    generateOrderID(e.tradeCounter),
		Symbol:     tick.Symbol,
		Side:       types.SideSell,
		Price:      sellPrice,
		Quantity:   quantity,
		Fee:        fee,
		Timestamp:  tick.Timestamp,
		ExchangeTS: tick.ExchangeTS,
	}
	e.trades = append(e.trades, trade)
}

func (e *Engine) calculateTotalValue(currentPrice int64) int64 {
	positions := e.positionMgr.GetAllPositions()
	totalPositionValue := int64(0)
	for _, pos := range positions {
		if pos.Quantity > 0 {
			totalPositionValue += types.CalcValue(pos.Quantity, currentPrice)
		}
	}
	return e.balance + totalPositionValue
}

func (e *Engine) calculateResult(ticks []types.Tick) *types.BacktestResult {
	finalPrice := int64(0)
	if len(ticks) > 0 {
		finalPrice = ticks[len(ticks)-1].Price
	}

	finalValue := e.calculateTotalValue(finalPrice)

	totalReturn := float64(finalValue-e.initialBalance) / float64(e.initialBalance)

	winRate, winningTrades, losingTrades := e.calculateWinRate()
	maxDrawdown, maxDrawdownStart, maxDrawdownEnd := e.calculateMaxDrawdown()
	sharpeRatio := e.calculateSharpeRatio()

	result := &types.BacktestResult{
		TotalReturn:      totalReturn,
		SharpeRatio:      sharpeRatio,
		MaxDrawdown:      maxDrawdown,
		WinRate:          winRate,
		TotalTrades:      len(e.trades) / 2,
		WinningTrades:    winningTrades,
		LosingTrades:     losingTrades,
		MaxDrawdownStart: maxDrawdownStart,
		MaxDrawdownEnd:   maxDrawdownEnd,
		FinalValue:       finalValue,
		InitialValue:     e.initialBalance,
		Trades:           e.trades,
		DailyPnL:         e.dailyPnL,
	}

	return result
}

func (e *Engine) calculateWinRate() (float64, int, int) {
	if len(e.trades) == 0 {
		return 0, 0, 0
	}

	type roundTrip struct {
		buyPrice   int64
		buyQty     int64
		buyFee     int64
		sellPrice  int64
		sellQty    int64
		sellFee    int64
		complete   bool
	}

	var roundTrips []roundTrip
	var current *roundTrip

	for _, trade := range e.trades {
		if trade.Side == types.SideBuy {
			if current != nil && current.complete {
				roundTrips = append(roundTrips, *current)
			}
			current = &roundTrip{
				buyPrice: trade.Price,
				buyQty:   trade.Quantity,
				buyFee:   trade.Fee,
			}
		} else if trade.Side == types.SideSell && current != nil && !current.complete {
			current.sellPrice = trade.Price
			current.sellQty = trade.Quantity
			current.sellFee = trade.Fee
			current.complete = true
		}
	}

	if current != nil && current.complete {
		roundTrips = append(roundTrips, *current)
	}

	winningTrades := 0
	losingTrades := 0

	for _, rt := range roundTrips {
		buyCost := types.CalcValue(rt.buyQty, rt.buyPrice) + rt.buyFee
		sellRevenue := types.CalcValue(rt.sellQty, rt.sellPrice) - rt.sellFee
		pnl := sellRevenue - buyCost

		if pnl > 0 {
			winningTrades++
		} else if pnl < 0 {
			losingTrades++
		}
	}

	total := winningTrades + losingTrades
	winRate := 0.0
	if total > 0 {
		winRate = float64(winningTrades) / float64(total)
	}

	return winRate, winningTrades, losingTrades
}

func (e *Engine) calculateMaxDrawdown() (float64, time.Time, time.Time) {
	if len(e.dailyPnL) == 0 {
		return 0, time.Time{}, time.Time{}
	}

	runningValues := make([]struct {
		date  time.Time
		value float64
	}, len(e.dailyPnL))

	for i, dp := range e.dailyPnL {
		runningValues[i] = struct {
			date  time.Time
			value float64
		}{
			date:  dp.Date,
			value: float64(dp.Value),
		}
	}

	maxDrawdown := 0.0
	peakValue := runningValues[0].value
	peakDate := runningValues[0].date
	maxDrawdownStart := peakDate
	maxDrawdownEnd := runningValues[0].date

	for _, rv := range runningValues {
		if rv.value > peakValue {
			peakValue = rv.value
			peakDate = rv.date
		}

		drawdown := (peakValue - rv.value) / peakValue
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
			maxDrawdownStart = peakDate
			maxDrawdownEnd = rv.date
		}
	}

	return maxDrawdown, maxDrawdownStart, maxDrawdownEnd
}

func (e *Engine) calculateSharpeRatio() float64 {
	if len(e.dailyPnL) < 2 {
		return 0
	}

	dailyReturns := make([]float64, 0, len(e.dailyPnL)-1)

	for i := 1; i < len(e.dailyPnL); i++ {
		prevValue := float64(e.dailyPnL[i-1].Value)
		currValue := float64(e.dailyPnL[i].Value)
		if prevValue > 0 {
			dailyReturn := (currValue - prevValue) / prevValue
			dailyReturns = append(dailyReturns, dailyReturn)
		}
	}

	if len(dailyReturns) == 0 {
		return 0
	}

	sum := 0.0
	for _, r := range dailyReturns {
		sum += r
	}
	meanReturn := sum / float64(len(dailyReturns))

	varianceSum := 0.0
	for _, r := range dailyReturns {
		diff := r - meanReturn
		varianceSum += diff * diff
	}
	stdDev := math.Sqrt(varianceSum / float64(len(dailyReturns)))

	if stdDev == 0 {
		return 0
	}

	sharpe := (meanReturn / stdDev) * math.Sqrt(252)

	return sharpe
}

func generateTradeID(counter int) string {
	return "BT-TRADE-" + itoa(counter)
}

func generateOrderID(counter int) string {
	return "BT-ORDER-" + itoa(counter)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
