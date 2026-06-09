package strategy

import "trading-bot/pkg/types"

type MACrossoverConfig struct {
	FastPeriod int
	SlowPeriod int
}

type MACrossover struct {
	BaseStrategy
	config   MACrossoverConfig
	prevFast float64
	prevSlow float64
}

func NewMACrossover(id, symbol string, config MACrossoverConfig) *MACrossover {
	return &MACrossover{
		BaseStrategy: BaseStrategy{id: id, symbol: symbol},
		config:       config,
	}
}

func (s *MACrossover) OnInit() {
	s.prices = make([]int64, 0)
	s.prevFast = 0
	s.prevSlow = 0
}

func (s *MACrossover) OnTick(tick types.Tick) types.SignalType {
	s.prices = append(s.prices, tick.Price)

	if len(s.prices) < s.config.SlowPeriod+1 {
		return types.SignalNone
	}

	fast := sma(s.prices, s.config.FastPeriod)
	slow := sma(s.prices, s.config.SlowPeriod)

	signal := types.SignalNone

	if s.prevFast > 0 && s.prevSlow > 0 {
		if s.prevFast <= s.prevSlow && fast > slow {
			signal = types.SignalBuy
		} else if s.prevFast >= s.prevSlow && fast < slow {
			signal = types.SignalSell
		}
	}

	s.prevFast = fast
	s.prevSlow = slow

	return signal
}

func (s *MACrossover) OnStop() {
}
