package strategy

import (
	"trading-bot/pkg/types"
)

type MACDConfig struct {
	FastEMAPeriod   int
	SlowEMAPeriod   int
	SignalPeriod    int
}

type MACD struct {
	BaseStrategy
	config     MACDConfig
	macdLine   []float64
	signalLine []float64
	prevMACD   float64
	prevSignal float64
}

func NewMACD(id, symbol string, config MACDConfig) *MACD {
	if config.FastEMAPeriod == 0 {
		config.FastEMAPeriod = 12
	}
	if config.SlowEMAPeriod == 0 {
		config.SlowEMAPeriod = 26
	}
	if config.SignalPeriod == 0 {
		config.SignalPeriod = 9
	}
	return &MACD{
		BaseStrategy: BaseStrategy{id: id, symbol: symbol},
		config:       config,
	}
}

func (s *MACD) OnInit() {
	s.prices = make([]int64, 0)
	s.macdLine = make([]float64, 0)
	s.signalLine = make([]float64, 0)
	s.prevMACD = 0
	s.prevSignal = 0
}

func (s *MACD) calculateMACD() float64 {
	fastEMA := ema(s.prices, s.config.FastEMAPeriod)
	slowEMA := ema(s.prices, s.config.SlowEMAPeriod)
	return fastEMA - slowEMA
}

func emaFloat(values []float64, period int) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(values) <= period {
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return sum / float64(len(values))
	}
	multiplier := 2.0 / float64(period+1)
	prev := emaFloat(values[:len(values)-1], period)
	current := values[len(values)-1]
	return current*multiplier + prev*(1-multiplier)
}

func (s *MACD) calculateSignalLine() float64 {
	if len(s.macdLine) < s.config.SignalPeriod {
		return 0
	}
	return emaFloat(s.macdLine, s.config.SignalPeriod)
}

func (s *MACD) OnTick(tick types.Tick) types.SignalType {
	s.prices = append(s.prices, tick.Price)

	if len(s.prices) < s.config.SlowEMAPeriod {
		return types.SignalNone
	}

	macd := s.calculateMACD()
	s.macdLine = append(s.macdLine, macd)

	if len(s.macdLine) < s.config.SignalPeriod {
		s.prevMACD = macd
		return types.SignalNone
	}

	signal := s.calculateSignalLine()
	s.signalLine = append(s.signalLine, signal)

	sig := types.SignalNone

	if s.prevMACD > 0 && s.prevSignal > 0 {
		if s.prevMACD <= s.prevSignal && macd > signal {
			sig = types.SignalBuy
		} else if s.prevMACD >= s.prevSignal && macd < signal {
			sig = types.SignalSell
		}
	}

	s.prevMACD = macd
	s.prevSignal = signal

	return sig
}

func (s *MACD) OnStop() {
}
