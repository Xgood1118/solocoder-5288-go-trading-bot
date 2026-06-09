package strategy

import "trading-bot/pkg/types"

type TurtleConfig struct {
	EntryPeriod int
	ExitPeriod  int
}

type Turtle struct {
	BaseStrategy
	config        TurtleConfig
	hasPosition   bool
	entryPrice    int64
	trailingStop  int64
	prevHigh      int64
	prevLow       int64
}

func NewTurtle(id, symbol string, config TurtleConfig) *Turtle {
	return &Turtle{
		BaseStrategy: BaseStrategy{id: id, symbol: symbol},
		config:       config,
	}
}

func (s *Turtle) OnInit() {
	s.prices = make([]int64, 0)
	s.hasPosition = false
	s.entryPrice = 0
	s.trailingStop = 0
	s.prevHigh = 0
	s.prevLow = 0
}

func (s *Turtle) OnTick(tick types.Tick) types.SignalType {
	s.prices = append(s.prices, tick.Price)

	high := maxPrice(s.prices, s.config.EntryPeriod)
	low := minPrice(s.prices, s.config.ExitPeriod)

	if len(s.prices) < s.config.EntryPeriod {
		s.prevHigh = high
		s.prevLow = low
		return types.SignalNone
	}

	signal := types.SignalNone

	if !s.hasPosition {
		if tick.Price > s.prevHigh && s.prevHigh > 0 {
			s.hasPosition = true
			s.entryPrice = tick.Price
			s.trailingStop = low
			signal = types.SignalBuy
		}
	} else {
		if tick.Price > s.trailingStop {
			s.trailingStop = low
		}
		if tick.Price <= s.trailingStop || tick.Price < s.prevLow {
			s.hasPosition = false
			signal = types.SignalSell
		}
	}

	s.prevHigh = high
	s.prevLow = low

	return signal
}

func (s *Turtle) OnStop() {
}
