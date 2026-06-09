package web

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (ws *WebServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ws.templates.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (ws *WebServer) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	bestBid, _ := ws.market.GetBestBid()
	bestAsk, _ := ws.market.GetBestAsk()
	currentPrice := int64(0)
	if bestBid > 0 && bestAsk > 0 {
		currentPrice = (bestBid + bestAsk) / 2
	}

	account := map[string]interface{}{
		"balance":      ws.liveEngine.GetBalance(),
		"equity":       ws.liveEngine.GetEquity(currentPrice),
		"currentPrice": currentPrice,
	}

	writeJSON(w, http.StatusOK, account)
}

func (ws *WebServer) handleGetPositions(w http.ResponseWriter, r *http.Request) {
	positions := ws.positionMgr.GetAllPositions()

	bestBid, _ := ws.market.GetBestBid()

	result := make([]map[string]interface{}, 0, len(positions))
	for _, pos := range positions {
		unrealizedPnL := ws.positionMgr.GetUnrealizedPnL(pos.Symbol, bestBid)
		result = append(result, map[string]interface{}{
			"symbol":        pos.Symbol,
			"quantity":      pos.Quantity,
			"avgPrice":      pos.AvgPrice,
			"realizedPnL":  pos.RealizedPnL,
			"unrealizedPnL": unrealizedPnL,
			"updatedAt":     pos.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func (ws *WebServer) handleGetOrders(w http.ResponseWriter, r *http.Request) {
	orders := ws.orderMgr.GetAllOrders()
	writeJSON(w, http.StatusOK, orders)
}

func (ws *WebServer) handleGetTrades(w http.ResponseWriter, r *http.Request) {
	trades := ws.reportGen.ListTradeDetails(100, 0)
	writeJSON(w, http.StatusOK, trades)
}

func (ws *WebServer) handleGetStrategies(w http.ResponseWriter, r *http.Request) {
	strategies := ws.liveEngine.GetStrategies()
	running := ws.liveEngine.IsRunning()

	result := make([]map[string]interface{}, 0, len(strategies))
	for _, s := range strategies {
		result = append(result, map[string]interface{}{
			"id":      s.GetID(),
			"symbol":  s.GetSymbol(),
			"running": running,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func (ws *WebServer) handleStartStrategy(w http.ResponseWriter, r *http.Request) {
	strategyID := chi.URLParam(r, "id")

	strategies := ws.liveEngine.GetStrategies()
	found := false
	for _, s := range strategies {
		if s.GetID() == strategyID {
			found = true
			break
		}
	}

	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "strategy not found"})
		return
	}

	if ws.liveEngine.IsRunning() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already running"})
		return
	}

	if err := ws.liveEngine.Start(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (ws *WebServer) handleStopStrategy(w http.ResponseWriter, r *http.Request) {
	strategyID := chi.URLParam(r, "id")

	strategies := ws.liveEngine.GetStrategies()
	found := false
	for _, s := range strategies {
		if s.GetID() == strategyID {
			found = true
			break
		}
	}

	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "strategy not found"})
		return
	}

	ws.liveEngine.Stop()

	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (ws *WebServer) handleGetRisk(w http.ResponseWriter, r *http.Request) {
	bestBid, _ := ws.market.GetBestBid()
	prices := map[string]int64{
		ws.market.Symbol(): bestBid,
	}

	riskInfo := map[string]interface{}{
		"dailyPnL":      ws.riskMgr.GetDailyPnL(),
		"circuitBroken":   ws.riskMgr.IsCircuitBroken(),
		"totalExposure":    ws.riskMgr.GetTotalExposure(prices),
	}

	writeJSON(w, http.StatusOK, riskInfo)
}

func (ws *WebServer) handleGetOrderBook(w http.ResponseWriter, r *http.Request) {
	ob := ws.market.GetDepth(20)
	writeJSON(w, http.StatusOK, ob)
}

func (ws *WebServer) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	bestBid, _ := ws.market.GetBestBid()
	bestAsk, _ := ws.market.GetBestAsk()
	currentPrice := int64(0)
	if bestBid > 0 && bestAsk > 0 {
		currentPrice = (bestBid + bestAsk) / 2
	}

	prices := map[string]int64{
		ws.market.Symbol(): bestBid,
	}
	summary := ws.reportGen.GetSummary(prices)

	metrics := map[string]interface{}{
		"balance":          summary.TotalBalance,
		"equity":           summary.TotalEquity,
		"daily_pnl":        summary.TodayPnL,
		"positions_count":  summary.OpenPositions,
		"total_trades":     summary.TotalTrades,
		"circuit_break":    ws.riskMgr.IsCircuitBroken(),
		"engine_running":   ws.liveEngine.IsRunning(),
		"current_price":    currentPrice,
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)

	for k, v := range metrics {
		switch val := v.(type) {
		case int64:
			w.Write([]byte(k + " " + itoa(val) + "\n"))
		case int:
			w.Write([]byte(k + " " + itoa(int64(val)) + "\n"))
		case bool:
			boolVal := 0
			if val {
				boolVal = 1
			}
			w.Write([]byte(k + " " + itoa(int64(boolVal)) + "\n"))
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
