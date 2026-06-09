package market

import (
	"fmt"
	"time"

	"trading-bot/pkg/types"
)

type MatchingEngine struct {
	orderBook *OrderBook
	tradeSeq  int64
}

func NewMatchingEngine(orderBook *OrderBook) *MatchingEngine {
	return &MatchingEngine{
		orderBook: orderBook,
	}
}

func (me *MatchingEngine) ProcessOrder(order *types.Order) ([]types.Trade, *types.Order) {
	me.orderBook.mu.Lock()
	defer me.orderBook.mu.Unlock()

	trades := make([]types.Trade, 0)

	if order == nil || order.Quantity <= 0 {
		return trades, order
	}

	if order.Status == 0 {
		order.Status = types.StatusOpen
	}
	order.CreatedAt = time.Now()
	order.UpdatedAt = time.Now()

	if order.Type == types.OrderTypeMarket {
		trades = me.matchMarketOrder(order)
	} else {
		trades = me.matchLimitOrder(order)
	}

	return trades, order
}

func (me *MatchingEngine) matchMarketOrder(order *types.Order) []types.Trade {
	trades := make([]types.Trade, 0)

	var oppSide types.OrderSide
	if order.Side == types.SideBuy {
		oppSide = types.SideSell
	} else {
		oppSide = types.SideBuy
	}

	remaining := order.Quantity - order.FilledQty

	for remaining > 0 {
		makerOrder, ok := me.orderBook.peekFrontOrder(oppSide)
		if !ok {
			break
		}

		makerRemaining := makerOrder.Quantity - makerOrder.FilledQty
		if makerRemaining <= 0 {
			me.orderBook.popFrontOrder(oppSide)
			continue
		}

		tradeQty := remaining
		if makerRemaining < tradeQty {
			tradeQty = makerRemaining
		}

		tradePrice := makerOrder.Price

		trade := me.createTrade(order, tradePrice, tradeQty)
		trades = append(trades, trade)

		me.orderBook.reduceOrderQty(makerOrder.ID, tradeQty)

		makerRemaining = makerOrder.Quantity - makerOrder.FilledQty
		if makerRemaining <= 0 {
			makerOrder.Status = types.StatusFilled
			me.orderBook.popFrontOrder(oppSide)
		} else {
			makerOrder.Status = types.StatusPartiallyFilled
		}

		order.FilledQty += tradeQty
		remaining -= tradeQty
	}

	if order.FilledQty >= order.Quantity {
		order.Status = types.StatusFilled
	} else if order.FilledQty > 0 {
		order.Status = types.StatusPartiallyFilled
	} else {
		order.Status = types.StatusRejected
	}

	order.UpdatedAt = time.Now()
	return trades
}

func (me *MatchingEngine) matchLimitOrder(order *types.Order) []types.Trade {
	trades := make([]types.Trade, 0)

	var oppSide types.OrderSide
	if order.Side == types.SideBuy {
		oppSide = types.SideSell
	} else {
		oppSide = types.SideBuy
	}

	remaining := order.Quantity - order.FilledQty

	for remaining > 0 {
		makerOrder, ok := me.orderBook.peekFrontOrder(oppSide)
		if !ok {
			break
		}

		if !me.canMatch(order, makerOrder) {
			break
		}

		makerRemaining := makerOrder.Quantity - makerOrder.FilledQty
		if makerRemaining <= 0 {
			me.orderBook.popFrontOrder(oppSide)
			continue
		}

		tradeQty := remaining
		if makerRemaining < tradeQty {
			tradeQty = makerRemaining
		}

		tradePrice := makerOrder.Price

		trade := me.createTrade(order, tradePrice, tradeQty)
		trades = append(trades, trade)

		me.orderBook.reduceOrderQty(makerOrder.ID, tradeQty)

		makerRemaining = makerOrder.Quantity - makerOrder.FilledQty
		if makerRemaining <= 0 {
			makerOrder.Status = types.StatusFilled
			me.orderBook.popFrontOrder(oppSide)
		} else {
			makerOrder.Status = types.StatusPartiallyFilled
		}

		order.FilledQty += tradeQty
		remaining -= tradeQty
	}

	if order.FilledQty >= order.Quantity {
		order.Status = types.StatusFilled
	} else {
		order.Status = types.StatusOpen
		me.addOrderToBook(order)
	}

	order.UpdatedAt = time.Now()
	return trades
}

func (me *MatchingEngine) canMatch(taker, maker *types.Order) bool {
	if taker.Side == types.SideBuy {
		return taker.Price >= maker.Price
	}
	return taker.Price <= maker.Price
}

func (me *MatchingEngine) createTrade(order *types.Order, price, qty int64) types.Trade {
	me.tradeSeq++
	tradeID := fmt.Sprintf("trade-%d", me.tradeSeq)

	return types.Trade{
		ID:         tradeID,
		OrderID:    order.ID,
		Symbol:     order.Symbol,
		Side:       order.Side,
		Price:      price,
		Quantity:   qty,
		Timestamp:  time.Now(),
		ExchangeTS: time.Now().UnixNano(),
	}
}

func (me *MatchingEngine) addOrderToBook(order *types.Order) {
	var tree *rbTree
	if order.Side == types.SideBuy {
		tree = me.orderBook.bids
	} else {
		tree = me.orderBook.asks
	}

	node := tree.insert(order.Price)
	remaining := order.Quantity - order.FilledQty
	node.level.totalQty += remaining
	elem := node.level.orders.PushBack(order)
	me.orderBook.orderMap[order.ID] = &orderEntry{
		order: order,
		elem:  elem,
	}
}

func (me *MatchingEngine) CancelOrder(orderID string) bool {
	return me.orderBook.CancelOrder(orderID)
}

func (me *MatchingEngine) OrderBook() *OrderBook {
	return me.orderBook
}
