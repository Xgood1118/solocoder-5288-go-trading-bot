package strategy

import (
	"trading-bot/pkg/types"
)

type GridConfig struct {
	GridCount   int
	GridSpacing float64
	BasePrice   int64
}

type Grid struct {
	BaseStrategy
	config       GridConfig
	gridPrices   []int64
	currentLevel int
	positionQty  int
}

func NewGrid(id, symbol string, config GridConfig) *Grid {
	return &Grid{
		BaseStrategy: BaseStrategy{id: id, symbol: symbol},
		config:       config,
	}
}

func (s *Grid) OnInit() {
	s.prices = make([]int64, 0)
	s.positionQty = 0
	s.currentLevel = 0
	s.buildGrid()
	if len(s.gridPrices) > 0 {
		s.currentLevel = s.findGridLevel(s.config.BasePrice)
	}
}

func (s *Grid) buildGrid() {
	half := s.config.GridCount / 2
	base := float64(s.config.BasePrice)
	s.gridPrices = make([]int64, s.config.GridCount)
	for i := 0; i < s.config.GridCount; i++ {
		offset := i - half
		price := base * (1 + float64(offset)*s.config.GridSpacing/100)
		s.gridPrices[i] = int64(price)
	}
}

func (s *Grid) findGridLevel(price int64) int {
	for i := len(s.gridPrices) - 1; i >= 0; i-- {
		if price >= s.gridPrices[i] {
			return i
		}
	}
	return 0
}

func (s *Grid) OnTick(tick types.Tick) types.SignalType {
	s.prices = append(s.prices, tick.Price)

	if len(s.gridPrices) == 0 {
		return types.SignalNone
	}

	newLevel := s.findGridLevel(tick.Price)
	signal := types.SignalNone

	if newLevel < s.currentLevel {
		s.positionQty++
		signal = types.SignalBuy
	} else if newLevel > s.currentLevel && s.positionQty > 0 {
		s.positionQty--
		signal = types.SignalSell
	}

	s.currentLevel = newLevel
	return signal
}

func (s *Grid) OnStop() {
}
