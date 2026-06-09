package strategy

import (
	"trading-bot/pkg/types"
)

type BollingerConfig struct {
	Period       int
	StdDevTimes  float64
}

type Bollinger struct {
	BaseStrategy
	config    BollingerConfig
	hasPosition bool
	entryPrice  int64
	midBand  float64
}

func NewBollinger(id, symbol string, config BollingerConfig) *Bollinger {
	if config.Period == 0 {
		config.Period = 20
	}
	if config.StdDevTimes == 0 {
		config.StdDevTimes = 2
	}
	return &Bollinger{
		BaseStrategy: BaseStrategy{id: id, symbol: symbol},
		config:       config,
	}
}

func (s *Bollinger) OnInit() {
	s.prices = make([]int64, 0)
	s.hasPosition = false
	s.entryPrice = 0
	s.midBand = 0
}

func (s *Bollinger) calculateBands() (float64, float64, float64) {
	mid := sma(s.prices, s.config.Period)
	std := stdDev(s.prices, s.config.Period)
	upper := mid + s.config.StdDevTimes*std
	lower := mid - s.config.StdDevTimes*std
	return upper, mid, lower
}

func (s *Bollinger) OnTick(tick types.Tick) types.SignalType {
	s.prices = append(s.prices, tick.Price)

	if len(s.prices) < s.config.Period {
		return types.SignalNone
	}

	upper, mid, lower := s.calculateBands()
	price := float64(tick.Price)

	signal := types.SignalNone

	if !s.hasPosition {
		if price < lower {
			s.hasPosition = true
			s.entryPrice = tick.Price
			s.midBand = mid
			signal = types.SignalBuy
		}
	} else {
		if price > upper {
			s.hasPosition = false
			signal = types.SignalSell
		} else if price < mid && s.entryPrice > tick.Price {
			s.hasPosition = false
			signal = types.SignalSell
		}
	}

	return signal
}

func (s *Bollinger) OnStop() {
}
