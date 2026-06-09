package report

import (
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"trading-bot/internal/order"
	"trading-bot/internal/position"
	"trading-bot/pkg/types"
)

var (
	ErrReportNotFound = errors.New("report not found")
	ErrInvalidDate    = errors.New("invalid date")
)

type DailyReport struct {
	Date          time.Time
	StartBalance  int64
	EndBalance    int64
	StartEquity   int64
	EndEquity     int64
	RealizedPnL   int64
	UnrealizedPnL int64
	TotalPnL      int64
	TradeCount    int
	Volume        int64
	Fees          int64
}

type PositionReport struct {
	Symbol        string
	Quantity      int64
	AvgPrice      int64
	CurrentPrice  int64
	MarketValue   int64
	UnrealizedPnL int64
	RealizedPnL   int64
	PnLPercent    float64
	Weight        float64
}

type StrategyReport struct {
	StrategyID   string
	StrategyName string
	Symbol       string
	TotalTrades  int
	WinningTrades int
	LosingTrades  int
	WinRate      float64
	TotalPnL     int64
	AvgWinPnL    int64
	AvgLossPnL   int64
	ProfitFactor float64
	MaxDrawdown  float64
	SharpeRatio  float64
}

type TradeDetail struct {
	TradeID    string
	OrderID    string
	Symbol     string
	Side       string
	Price      int64
	Quantity   int64
	Fee        int64
	Timestamp  time.Time
	PnL        int64
	PnLPercent float64
}

type SummaryReport struct {
	TotalEquity        int64
	TotalBalance       int64
	TotalPositionValue int64
	TotalRealizedPnL   int64
	TotalUnrealizedPnL int64
	TodayPnL           int64
	TotalTrades        int
	OpenPositions      int
	RiskExposure       int64
	RiskExposurePct    float64
}

type fifoLot struct {
	qty   int64
	price int64
}

type ReportGenerator struct {
	positionMgr *position.Manager
	orderMgr    *order.Manager
	trades      []types.Trade
	dailyPnL    []types.DailyPnL
	tradePnLMap map[string]int64
	mu          sync.RWMutex

	balance int64
	equity  int64
}

func NewGenerator(posMgr *position.Manager, orderMgr *order.Manager) *ReportGenerator {
	return &ReportGenerator{
		positionMgr: posMgr,
		orderMgr:    orderMgr,
		trades:      make([]types.Trade, 0),
		dailyPnL:    make([]types.DailyPnL, 0),
		tradePnLMap: make(map[string]int64),
	}
}

func (g *ReportGenerator) SetBalance(balance int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.balance = balance
}

func (g *ReportGenerator) SetEquity(equity int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.equity = equity
}

func (g *ReportGenerator) AddTrade(trade types.Trade) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trades = append(g.trades, trade)
	g.recalculateTradePnL()
}

func (g *ReportGenerator) RecordDailyPnL(date time.Time, pnl int64, equity int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	date = date.Truncate(24 * time.Hour)

	for i, dp := range g.dailyPnL {
		if dp.Date.Equal(date) {
			g.dailyPnL[i].PnL = pnl
			g.dailyPnL[i].Value = equity
			return
		}
	}

	g.dailyPnL = append(g.dailyPnL, types.DailyPnL{
		Date:  date,
		PnL:   pnl,
		Value: equity,
	})

	sort.Slice(g.dailyPnL, func(i, j int) bool {
		return g.dailyPnL[i].Date.Before(g.dailyPnL[j].Date)
	})
}

func (g *ReportGenerator) GetDailyReport(date time.Time) (*DailyReport, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	date = date.Truncate(24 * time.Hour)

	var targetDaily *types.DailyPnL
	var prevDaily *types.DailyPnL

	for i, dp := range g.dailyPnL {
		if dp.Date.Equal(date) {
			targetDaily = &g.dailyPnL[i]
			if i > 0 {
				prevDaily = &g.dailyPnL[i-1]
			}
			break
		}
	}

	if targetDaily == nil {
		return nil, ErrReportNotFound
	}

	startEquity := g.equity
	if prevDaily != nil {
		startEquity = prevDaily.Value
	}

	dayTrades, dayVolume, dayFees := g.getDayTrades(date)

	realizedPnL := g.getDayRealizedPnL(date)
	unrealizedPnL := targetDaily.PnL - realizedPnL

	return &DailyReport{
		Date:          date,
		StartBalance:  g.balance,
		EndBalance:    g.balance,
		StartEquity:   startEquity,
		EndEquity:     targetDaily.Value,
		RealizedPnL:   realizedPnL,
		UnrealizedPnL: unrealizedPnL,
		TotalPnL:      targetDaily.PnL,
		TradeCount:    len(dayTrades),
		Volume:        dayVolume,
		Fees:          dayFees,
	}, nil
}

func (g *ReportGenerator) GetPositionReport(prices map[string]int64) []PositionReport {
	g.mu.RLock()
	defer g.mu.RUnlock()

	positions := g.positionMgr.GetAllPositions()
	reports := make([]PositionReport, 0, len(positions))

	totalMarketValue := int64(0)
	for symbol, pos := range positions {
		if pos.Quantity == 0 {
			continue
		}
		price, ok := prices[symbol]
		if !ok {
			continue
		}
		marketValue := types.CalcValue(pos.Quantity, price)
		if marketValue < 0 {
			marketValue = -marketValue
		}
		totalMarketValue += marketValue
	}

	for symbol, pos := range positions {
		if pos.Quantity == 0 {
			continue
		}
		price, ok := prices[symbol]
		if !ok {
			continue
		}

		marketValue := types.CalcValue(pos.Quantity, price)
		unrealizedPnL := g.positionMgr.GetUnrealizedPnL(symbol, price)

		pnlPercent := 0.0
		if pos.AvgPrice > 0 && pos.Quantity > 0 {
			pnlPercent = float64(price-pos.AvgPrice) / float64(pos.AvgPrice)
		} else if pos.AvgPrice > 0 && pos.Quantity < 0 {
			pnlPercent = float64(pos.AvgPrice-price) / float64(pos.AvgPrice)
		}

		weight := 0.0
		absMarketValue := marketValue
		if absMarketValue < 0 {
			absMarketValue = -absMarketValue
		}
		if totalMarketValue > 0 {
			weight = float64(absMarketValue) / float64(totalMarketValue)
		}

		reports = append(reports, PositionReport{
			Symbol:        symbol,
			Quantity:      pos.Quantity,
			AvgPrice:      pos.AvgPrice,
			CurrentPrice:  price,
			MarketValue:   marketValue,
			UnrealizedPnL: unrealizedPnL,
			RealizedPnL:   pos.RealizedPnL,
			PnLPercent:    pnlPercent,
			Weight:        weight,
		})
	}

	sort.Slice(reports, func(i, j int) bool {
		return reports[i].Symbol < reports[j].Symbol
	})

	return reports
}

func (g *ReportGenerator) GetStrategyReport(strategyID string, prices map[string]int64) (*StrategyReport, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	strategyTrades := g.getStrategyTrades(strategyID)
	if len(strategyTrades) == 0 {
		return nil, ErrReportNotFound
	}

	symbol := ""
	strategyName := strategyID

	winningTrades := 0
	losingTrades := 0
	totalWinPnL := int64(0)
	totalLossPnL := int64(0)
	totalPnL := int64(0)
	pnlList := make([]int64, 0)

	for _, trade := range strategyTrades {
		if symbol == "" {
			symbol = trade.Symbol
		}
		pnl, ok := g.tradePnLMap[trade.ID]
		if !ok {
			continue
		}
		pnlList = append(pnlList, pnl)
		totalPnL += pnl

		if pnl > 0 {
			winningTrades++
			totalWinPnL += pnl
		} else if pnl < 0 {
			losingTrades++
			totalLossPnL += pnl
		}
	}

	totalTrades := winningTrades + losingTrades
	winRate := 0.0
	if totalTrades > 0 {
		winRate = float64(winningTrades) / float64(totalTrades)
	}

	avgWinPnL := int64(0)
	if winningTrades > 0 {
		avgWinPnL = totalWinPnL / int64(winningTrades)
	}

	avgLossPnL := int64(0)
	if losingTrades > 0 {
		avgLossPnL = totalLossPnL / int64(losingTrades)
	}

	profitFactor := 0.0
	if totalLossPnL != 0 {
		profitFactor = float64(totalWinPnL) / float64(-totalLossPnL)
	}

	maxDrawdown := g.calculateMaxDrawdown(strategyID)
	sharpeRatio := g.calculateSharpeRatio(strategyID)

	return &StrategyReport{
		StrategyID:    strategyID,
		StrategyName:  strategyName,
		Symbol:        symbol,
		TotalTrades:   totalTrades,
		WinningTrades: winningTrades,
		LosingTrades:  losingTrades,
		WinRate:       winRate,
		TotalPnL:      totalPnL,
		AvgWinPnL:     avgWinPnL,
		AvgLossPnL:    avgLossPnL,
		ProfitFactor:  profitFactor,
		MaxDrawdown:   maxDrawdown,
		SharpeRatio:   sharpeRatio,
	}, nil
}

func (g *ReportGenerator) GetAllStrategyReports(prices map[string]int64) []StrategyReport {
	g.mu.RLock()
	defer g.mu.RUnlock()

	strategyIDs := g.getAllStrategyIDs()
	reports := make([]StrategyReport, 0, len(strategyIDs))

	for _, strategyID := range strategyIDs {
		report, err := g.GetStrategyReport(strategyID, prices)
		if err == nil {
			reports = append(reports, *report)
		}
	}

	return reports
}

func (g *ReportGenerator) GetTradeDetail(tradeID string) (*TradeDetail, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, trade := range g.trades {
		if trade.ID == tradeID {
			pnl := g.tradePnLMap[tradeID]
			pnlPercent := 0.0
			if trade.Side == types.SideSell && trade.Price > 0 {
				avgCost := g.getAvgCostForTrade(trade)
				costValue := types.CalcValue(trade.Quantity, avgCost)
				if costValue > 0 {
					pnlPercent = float64(pnl) / float64(costValue)
				}
			}

			return &TradeDetail{
				TradeID:    trade.ID,
				OrderID:    trade.OrderID,
				Symbol:     trade.Symbol,
				Side:       trade.Side.String(),
				Price:      trade.Price,
				Quantity:   trade.Quantity,
				Fee:        trade.Fee,
				Timestamp:  trade.Timestamp,
				PnL:        pnl,
				PnLPercent: pnlPercent,
			}, nil
		}
	}

	return nil, ErrReportNotFound
}

func (g *ReportGenerator) ListTradeDetails(limit, offset int) []TradeDetail {
	g.mu.RLock()
	defer g.mu.RUnlock()

	sortedTrades := make([]types.Trade, len(g.trades))
	copy(sortedTrades, g.trades)
	sort.Slice(sortedTrades, func(i, j int) bool {
		return sortedTrades[i].Timestamp.After(sortedTrades[j].Timestamp)
	})

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(sortedTrades)
	}

	end := offset + limit
	if end > len(sortedTrades) {
		end = len(sortedTrades)
	}
	if offset >= len(sortedTrades) {
		return []TradeDetail{}
	}

	trades := sortedTrades[offset:end]
	details := make([]TradeDetail, 0, len(trades))

	for _, trade := range trades {
		pnl := g.tradePnLMap[trade.ID]
		pnlPercent := 0.0
		if trade.Side == types.SideSell && trade.Price > 0 {
			avgCost := g.getAvgCostForTrade(trade)
			costValue := types.CalcValue(trade.Quantity, avgCost)
			if costValue > 0 {
				pnlPercent = float64(pnl) / float64(costValue)
			}
		}

		details = append(details, TradeDetail{
			TradeID:    trade.ID,
			OrderID:    trade.OrderID,
			Symbol:     trade.Symbol,
			Side:       trade.Side.String(),
			Price:      trade.Price,
			Quantity:   trade.Quantity,
			Fee:        trade.Fee,
			Timestamp:  trade.Timestamp,
			PnL:        pnl,
			PnLPercent: pnlPercent,
		})
	}

	return details
}

func (g *ReportGenerator) ListTradeDetailsByStrategy(strategyID string, limit, offset int) []TradeDetail {
	g.mu.RLock()
	defer g.mu.RUnlock()

	strategyTrades := g.getStrategyTrades(strategyID)
	sort.Slice(strategyTrades, func(i, j int) bool {
		return strategyTrades[i].Timestamp.After(strategyTrades[j].Timestamp)
	})

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(strategyTrades)
	}

	end := offset + limit
	if end > len(strategyTrades) {
		end = len(strategyTrades)
	}
	if offset >= len(strategyTrades) {
		return []TradeDetail{}
	}

	trades := strategyTrades[offset:end]
	details := make([]TradeDetail, 0, len(trades))

	for _, trade := range trades {
		pnl := g.tradePnLMap[trade.ID]
		pnlPercent := 0.0
		if trade.Side == types.SideSell && trade.Price > 0 {
			avgCost := g.getAvgCostForTrade(trade)
			costValue := types.CalcValue(trade.Quantity, avgCost)
			if costValue > 0 {
				pnlPercent = float64(pnl) / float64(costValue)
			}
		}

		details = append(details, TradeDetail{
			TradeID:    trade.ID,
			OrderID:    trade.OrderID,
			Symbol:     trade.Symbol,
			Side:       trade.Side.String(),
			Price:      trade.Price,
			Quantity:   trade.Quantity,
			Fee:        trade.Fee,
			Timestamp:  trade.Timestamp,
			PnL:        pnl,
			PnLPercent: pnlPercent,
		})
	}

	return details
}

func (g *ReportGenerator) GetSummary(prices map[string]int64) *SummaryReport {
	g.mu.RLock()
	defer g.mu.RUnlock()

	totalPositionValue := int64(0)
	totalRealizedPnL := int64(0)
	totalUnrealizedPnL := int64(0)
	openPositions := 0

	positions := g.positionMgr.GetAllPositions()
	for symbol, pos := range positions {
		if pos.Quantity == 0 {
			continue
		}
		openPositions++

		price, ok := prices[symbol]
		if ok {
			marketValue := types.CalcValue(pos.Quantity, price)
			if marketValue < 0 {
				marketValue = -marketValue
			}
			totalPositionValue += marketValue

			unrealizedPnL := g.positionMgr.GetUnrealizedPnL(symbol, price)
			totalUnrealizedPnL += unrealizedPnL
		}

		totalRealizedPnL += pos.RealizedPnL
	}

	totalEquity := g.balance + totalPositionValue
	riskExposure := g.positionMgr.GetTotalExposure(prices)
	riskExposurePct := 0.0
	if totalEquity > 0 {
		riskExposurePct = float64(riskExposure) / float64(totalEquity)
	}

	today := time.Now().Truncate(24 * time.Hour)
	todayPnL := g.getDayRealizedPnL(today)
	for _, dp := range g.dailyPnL {
		if dp.Date.Equal(today) {
			todayPnL = dp.PnL
			break
		}
	}

	return &SummaryReport{
		TotalEquity:        totalEquity,
		TotalBalance:       g.balance,
		TotalPositionValue: totalPositionValue,
		TotalRealizedPnL:   totalRealizedPnL,
		TotalUnrealizedPnL: totalUnrealizedPnL,
		TodayPnL:           todayPnL,
		TotalTrades:        len(g.trades),
		OpenPositions:      openPositions,
		RiskExposure:       riskExposure,
		RiskExposurePct:    riskExposurePct,
	}
}

func (g *ReportGenerator) GenerateCSVReport(reportType string, startDate, endDate time.Time) (string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	startDate = startDate.Truncate(24 * time.Hour)
	endDate = endDate.Truncate(24 * time.Hour).Add(24*time.Hour - time.Second)

	switch strings.ToLower(reportType) {
	case "daily":
		return g.generateDailyCSV(startDate, endDate)
	case "trades":
		return g.generateTradesCSV(startDate, endDate)
	case "positions":
		return g.generatePositionsCSV()
	default:
		return "", fmt.Errorf("unsupported report type: %s", reportType)
	}
}

func (g *ReportGenerator) generateDailyCSV(startDate, endDate time.Time) (string, error) {
	var buf strings.Builder
	writer := csv.NewWriter(&buf)

	header := []string{"Date", "StartEquity", "EndEquity", "TotalPnL", "RealizedPnL", "UnrealizedPnL", "TradeCount", "Volume", "Fees"}
	if err := writer.Write(header); err != nil {
		return "", err
	}

	for _, dp := range g.dailyPnL {
		if (dp.Date.Equal(startDate) || dp.Date.After(startDate)) && (dp.Date.Equal(endDate) || dp.Date.Before(endDate)) {
			dayTrades, dayVolume, dayFees := g.getDayTrades(dp.Date)
			realizedPnL := g.getDayRealizedPnL(dp.Date)
			unrealizedPnL := dp.PnL - realizedPnL

			row := []string{
				dp.Date.Format("2006-01-02"),
				strconv.FormatInt(dp.Value-dp.PnL, 10),
				strconv.FormatInt(dp.Value, 10),
				strconv.FormatInt(dp.PnL, 10),
				strconv.FormatInt(realizedPnL, 10),
				strconv.FormatInt(unrealizedPnL, 10),
				strconv.Itoa(len(dayTrades)),
				strconv.FormatInt(dayVolume, 10),
				strconv.FormatInt(dayFees, 10),
			}
			if err := writer.Write(row); err != nil {
				return "", err
			}
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (g *ReportGenerator) generateTradesCSV(startDate, endDate time.Time) (string, error) {
	var buf strings.Builder
	writer := csv.NewWriter(&buf)

	header := []string{"TradeID", "OrderID", "Symbol", "Side", "Price", "Quantity", "Fee", "Timestamp", "PnL", "PnLPercent"}
	if err := writer.Write(header); err != nil {
		return "", err
	}

	for _, trade := range g.trades {
		if (trade.Timestamp.Equal(startDate) || trade.Timestamp.After(startDate)) &&
			(trade.Timestamp.Equal(endDate) || trade.Timestamp.Before(endDate)) {
			pnl := g.tradePnLMap[trade.ID]
			pnlPercent := 0.0
			if trade.Side == types.SideSell && trade.Price > 0 {
				avgCost := g.getAvgCostForTrade(trade)
				costValue := types.CalcValue(trade.Quantity, avgCost)
				if costValue > 0 {
					pnlPercent = float64(pnl) / float64(costValue)
				}
			}

			row := []string{
				trade.ID,
				trade.OrderID,
				trade.Symbol,
				trade.Side.String(),
				strconv.FormatInt(trade.Price, 10),
				strconv.FormatInt(trade.Quantity, 10),
				strconv.FormatInt(trade.Fee, 10),
				trade.Timestamp.Format(time.RFC3339),
				strconv.FormatInt(pnl, 10),
				strconv.FormatFloat(pnlPercent, 'f', 6, 64),
			}
			if err := writer.Write(row); err != nil {
				return "", err
			}
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (g *ReportGenerator) generatePositionsCSV() (string, error) {
	var buf strings.Builder
	writer := csv.NewWriter(&buf)

	header := []string{"Symbol", "Quantity", "AvgPrice", "MarketValue", "UnrealizedPnL", "RealizedPnL", "PnLPercent"}
	if err := writer.Write(header); err != nil {
		return "", err
	}

	positions := g.positionMgr.GetAllPositions()
	for symbol, pos := range positions {
		if pos.Quantity == 0 {
			continue
		}

		marketValue := types.CalcValue(pos.Quantity, pos.AvgPrice)
		unrealizedPnL := int64(0)
		pnlPercent := 0.0

		row := []string{
			symbol,
			strconv.FormatInt(pos.Quantity, 10),
			strconv.FormatInt(pos.AvgPrice, 10),
			strconv.FormatInt(marketValue, 10),
			strconv.FormatInt(unrealizedPnL, 10),
			strconv.FormatInt(pos.RealizedPnL, 10),
			strconv.FormatFloat(pnlPercent, 'f', 6, 64),
		}
		if err := writer.Write(row); err != nil {
			return "", err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (g *ReportGenerator) getDayTrades(date time.Time) ([]types.Trade, int64, int64) {
	date = date.Truncate(24 * time.Hour)
	nextDay := date.Add(24 * time.Hour)

	var dayTrades []types.Trade
	volume := int64(0)
	fees := int64(0)

	for _, trade := range g.trades {
		if (trade.Timestamp.Equal(date) || trade.Timestamp.After(date)) && trade.Timestamp.Before(nextDay) {
			dayTrades = append(dayTrades, trade)
			volume += trade.Quantity
			fees += trade.Fee
		}
	}

	return dayTrades, volume, fees
}

func (g *ReportGenerator) getDayRealizedPnL(date time.Time) int64 {
	date = date.Truncate(24 * time.Hour)
	nextDay := date.Add(24 * time.Hour)

	realizedPnL := int64(0)
	for _, trade := range g.trades {
		if (trade.Timestamp.Equal(date) || trade.Timestamp.After(date)) && trade.Timestamp.Before(nextDay) {
			if pnl, ok := g.tradePnLMap[trade.ID]; ok {
				realizedPnL += pnl
			}
		}
	}
	return realizedPnL
}

func (g *ReportGenerator) recalculateTradePnL() {
	g.tradePnLMap = make(map[string]int64)

	symbolBuys := make(map[string][]fifoLot)

	sortedTrades := make([]types.Trade, len(g.trades))
	copy(sortedTrades, g.trades)
	sort.Slice(sortedTrades, func(i, j int) bool {
		return sortedTrades[i].Timestamp.Before(sortedTrades[j].Timestamp)
	})

	for _, trade := range sortedTrades {
		if trade.Side == types.SideBuy {
			symbolBuys[trade.Symbol] = append(symbolBuys[trade.Symbol], fifoLot{
				qty:   trade.Quantity,
				price: trade.Price,
			})
		} else if trade.Side == types.SideSell {
			pnl := g.matchFifoSell(trade, symbolBuys[trade.Symbol])
			g.tradePnLMap[trade.ID] = pnl
		}
	}
}

func (g *ReportGenerator) matchFifoSell(trade types.Trade, buys []fifoLot) int64 {
	if len(buys) == 0 {
		return 0
	}

	remainingQty := trade.Quantity
	totalPnL := int64(0)

	for remainingQty > 0 && len(buys) > 0 {
		if buys[0].qty <= remainingQty {
			pnl := types.MulDiv(buys[0].qty, trade.Price - buys[0].price, types.SatoshiScale)
			totalPnL += pnl
			remainingQty -= buys[0].qty
			buys = buys[1:]
		} else {
			pnl := types.MulDiv(remainingQty, trade.Price - buys[0].price, types.SatoshiScale)
			totalPnL += pnl
			buys[0].qty -= remainingQty
			remainingQty = 0
		}
	}

	return totalPnL
}

func (g *ReportGenerator) getStrategyTrades(strategyID string) []types.Trade {
	orders := g.orderMgr.GetOrdersByStrategy(strategyID)
	orderIDMap := make(map[string]bool)
	for _, o := range orders {
		orderIDMap[o.ID] = true
	}

	var strategyTrades []types.Trade
	for _, trade := range g.trades {
		if orderIDMap[trade.OrderID] {
			strategyTrades = append(strategyTrades, trade)
		}
	}

	return strategyTrades
}

func (g *ReportGenerator) getAllStrategyIDs() []string {
	orders := g.orderMgr.GetAllOrders()
	strategyMap := make(map[string]bool)
	for _, o := range orders {
		if o.StrategyID != "" {
			strategyMap[o.StrategyID] = true
		}
	}

	strategyIDs := make([]string, 0, len(strategyMap))
	for id := range strategyMap {
		strategyIDs = append(strategyIDs, id)
	}

	sort.Strings(strategyIDs)
	return strategyIDs
}

func (g *ReportGenerator) getAvgCostForTrade(trade types.Trade) int64 {
	if trade.Side != types.SideSell {
		return 0
	}

	symbolBuys := make(map[string][]fifoLot)

	sortedTrades := make([]types.Trade, len(g.trades))
	copy(sortedTrades, g.trades)
	sort.Slice(sortedTrades, func(i, j int) bool {
		return sortedTrades[i].Timestamp.Before(sortedTrades[j].Timestamp)
	})

	for _, t := range sortedTrades {
		if t.ID == trade.ID {
			break
		}
		if t.Symbol != trade.Symbol {
			continue
		}
		if t.Side == types.SideBuy {
			symbolBuys[t.Symbol] = append(symbolBuys[t.Symbol], fifoLot{
				qty:   t.Quantity,
				price: t.Price,
			})
		} else if t.Side == types.SideSell {
			remainingQty := t.Quantity
			for remainingQty > 0 && len(symbolBuys[t.Symbol]) > 0 {
				if symbolBuys[t.Symbol][0].qty <= remainingQty {
					remainingQty -= symbolBuys[t.Symbol][0].qty
					symbolBuys[t.Symbol] = symbolBuys[t.Symbol][1:]
				} else {
					symbolBuys[t.Symbol][0].qty -= remainingQty
					remainingQty = 0
				}
			}
		}
	}

	buys := symbolBuys[trade.Symbol]
	if len(buys) == 0 {
		return 0
	}

	totalCost := int64(0)
	totalQty := int64(0)
	remainingQty := trade.Quantity

	for remainingQty > 0 && len(buys) > 0 {
		if buys[0].qty <= remainingQty {
			totalCost += buys[0].price * buys[0].qty
			totalQty += buys[0].qty
			remainingQty -= buys[0].qty
			buys = buys[1:]
		} else {
			totalCost += buys[0].price * remainingQty
			totalQty += remainingQty
			remainingQty = 0
		}
	}

	if totalQty == 0 {
		return 0
	}

	return totalCost / totalQty
}

func (g *ReportGenerator) calculateMaxDrawdown(strategyID string) float64 {
	strategyTrades := g.getStrategyTrades(strategyID)
	if len(strategyTrades) == 0 {
		return 0
	}

	sort.Slice(strategyTrades, func(i, j int) bool {
		return strategyTrades[i].Timestamp.Before(strategyTrades[j].Timestamp)
	})

	cumulativePnL := int64(0)
	peakPnL := int64(0)
	maxDrawdown := 0.0

	for _, trade := range strategyTrades {
		pnl, ok := g.tradePnLMap[trade.ID]
		if !ok {
			continue
		}
		cumulativePnL += pnl

		if cumulativePnL > peakPnL {
			peakPnL = cumulativePnL
		}

		drawdown := 0.0
		if peakPnL > 0 {
			drawdown = float64(peakPnL-cumulativePnL) / float64(peakPnL)
		}

		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}

	return maxDrawdown
}

func (g *ReportGenerator) calculateSharpeRatio(strategyID string) float64 {
	strategyTrades := g.getStrategyTrades(strategyID)
	if len(strategyTrades) < 2 {
		return 0
	}

	pnlList := make([]float64, 0)
	for _, trade := range strategyTrades {
		if pnl, ok := g.tradePnLMap[trade.ID]; ok {
			pnlList = append(pnlList, float64(pnl))
		}
	}

	if len(pnlList) < 2 {
		return 0
	}

	sum := 0.0
	for _, pnl := range pnlList {
		sum += pnl
	}
	mean := sum / float64(len(pnlList))

	variance := 0.0
	for _, pnl := range pnlList {
		diff := pnl - mean
		variance += diff * diff
	}
	variance /= float64(len(pnlList) - 1)
	stdDev := math.Sqrt(variance)

	if stdDev == 0 {
		return 0
	}

	sharpe := mean / stdDev

	return sharpe
}
