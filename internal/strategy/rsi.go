package strategy

import (
	"math"
	"trading-bot/pkg/types"
)

type RSIConfig struct {
	Period       int
	Overbought   float64
	Oversold     float64
}

type RSI struct {
	BaseStrategy
	config  RSIConfig
	prevRSI float64
}

func NewRSI(id, symbol string, config RSIConfig) *RSI {
	if config.Period == 0 {
		config.Period = 14
	}
	if config.Overbought == 0 {
		config.Overbought = 70
	}
	if config.Oversold == 0 {
		config.Oversold = 30
	}
	return &RSI{
		BaseStrategy: BaseStrategy{id: id, symbol: symbol},
		config:       config,
	}
}

func (s *RSI) OnInit() {
	s.prices = make([]int64, 0)
	s.prevRSI = 0
}

func (s *RSI) calculateRSI() float64 {
	if len(s.prices) < s.config.Period+1 {
		return 50
	}

	period := s.config.Period
	gains := 0.0
	losses := 0.0

	start := len(s.prices) - period
	for i := start; i < len(s.prices); i++ {
		diff := float64(s.prices[i]) - float64(s.prices[i-1])
		if diff > 0 {
			gains += diff
		} else {
			losses += math.Abs(diff)
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))
	return rsi
}

func (s *RSI) OnTick(tick types.Tick) types.SignalType {
	s.prices = append(s.prices, tick.Price)

	if len(s.prices) < s.config.Period+1 {
		return types.SignalNone
	}

	rsi := s.calculateRSI()
	signal := types.SignalNone

	if rsi < s.config.Oversold && s.prevRSI >= s.config.Oversold {
		signal = types.SignalBuy
	} else if rsi > s.config.Overbought && s.prevRSI <= s.config.Overbought {
		signal = types.SignalSell
	}

	s.prevRSI = rsi
	return signal
}

func (s *RSI) OnStop() {
}
