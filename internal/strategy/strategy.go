package strategy

import (
	"math"
	"trading-bot/pkg/types"
)

type BaseStrategy struct {
	id     string
	symbol string
	prices []int64
}

func (b *BaseStrategy) Init(id, symbol string) {
	b.id = id
	b.symbol = symbol
	b.prices = make([]int64, 0)
}

func (b *BaseStrategy) AddPrice(price int64) {
	b.prices = append(b.prices, price)
}

func (b *BaseStrategy) GetID() string {
	return b.id
}

func (b *BaseStrategy) GetSymbol() string {
	return b.symbol
}

func (b *BaseStrategy) GetPrices() []int64 {
	return b.prices
}

func sma(prices []int64, period int) float64 {
	if len(prices) < period {
		return 0
	}
	start := len(prices) - period
	sum := 0.0
	for i := start; i < len(prices); i++ {
		sum += float64(prices[i])
	}
	return sum / float64(period)
}

func ema(prices []int64, period int) float64 {
	if len(prices) == 0 {
		return 0
	}
	if len(prices) <= period {
		return sma(prices, len(prices))
	}
	multiplier := 2.0 / float64(period+1)
	emaPrev := sma(prices[:len(prices)-1], period)
	current := float64(prices[len(prices)-1])
	return current*multiplier + emaPrev*(1-multiplier)
}

func stdDev(prices []int64, period int) float64 {
	if len(prices) < period {
		return 0
	}
	start := len(prices) - period
	mean := sma(prices, period)
	sum := 0.0
	for i := start; i < len(prices); i++ {
		diff := float64(prices[i]) - mean
		sum += diff * diff
	}
	return math.Sqrt(sum / float64(period))
}

func maxPrice(prices []int64, period int) int64 {
	if len(prices) < period {
		return 0
	}
	start := len(prices) - period
	max := prices[start]
	for i := start + 1; i < len(prices); i++ {
		if prices[i] > max {
			max = prices[i]
		}
	}
	return max
}

func minPrice(prices []int64, period int) int64 {
	if len(prices) < period {
		return 0
	}
	start := len(prices) - period
	min := prices[start]
	for i := start + 1; i < len(prices); i++ {
		if prices[i] < min {
			min = prices[i]
		}
	}
	return min
}

var _ types.Strategy = (*MACrossover)(nil)
var _ types.Strategy = (*Turtle)(nil)
var _ types.Strategy = (*RSI)(nil)
var _ types.Strategy = (*MACD)(nil)
var _ types.Strategy = (*Bollinger)(nil)
var _ types.Strategy = (*Grid)(nil)
