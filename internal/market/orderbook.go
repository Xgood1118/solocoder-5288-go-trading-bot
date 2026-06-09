package market

import (
	"container/list"
	"sync"
	"time"

	"trading-bot/pkg/types"
)

const (
	red   = true
	black = false
)

type rbNode struct {
	price  int64
	color  bool
	left   *rbNode
	right  *rbNode
	parent *rbNode
	level  *priceLevel
}

type priceLevel struct {
	price    int64
	totalQty int64
	orders   *list.List
}

type orderEntry struct {
	order *types.Order
	elem  *list.Element
}

type rbTree struct {
	root *rbNode
	size int
	desc bool
}

type OrderBook struct {
	mu       sync.RWMutex
	symbol   string
	bids     *rbTree
	asks     *rbTree
	orderMap map[string]*orderEntry
}

func newPriceLevel(price int64) *priceLevel {
	return &priceLevel{
		price:  price,
		orders: list.New(),
	}
}

func newRBTree(desc bool) *rbTree {
	return &rbTree{
		desc: desc,
	}
}

func (t *rbTree) compare(a, b int64) int {
	if t.desc {
		if a > b {
			return -1
		}
		if a < b {
			return 1
		}
		return 0
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func (t *rbTree) grandparent(n *rbNode) *rbNode {
	if n != nil && n.parent != nil {
		return n.parent.parent
	}
	return nil
}

func (t *rbTree) uncle(n *rbNode) *rbNode {
	gp := t.grandparent(n)
	if gp == nil {
		return nil
	}
	if n.parent == gp.left {
		return gp.right
	}
	return gp.left
}

func (t *rbTree) sibling(n *rbNode) *rbNode {
	if n == nil || n.parent == nil {
		return nil
	}
	if n == n.parent.left {
		return n.parent.right
	}
	return n.parent.left
}

func (t *rbTree) rotateLeft(n *rbNode) {
	r := n.right
	n.right = r.left
	if r.left != nil {
		r.left.parent = n
	}
	r.parent = n.parent
	if n.parent == nil {
		t.root = r
	} else if n == n.parent.left {
		n.parent.left = r
	} else {
		n.parent.right = r
	}
	r.left = n
	n.parent = r
}

func (t *rbTree) rotateRight(n *rbNode) {
	l := n.left
	n.left = l.right
	if l.right != nil {
		l.right.parent = n
	}
	l.parent = n.parent
	if n.parent == nil {
		t.root = l
	} else if n == n.parent.right {
		n.parent.right = l
	} else {
		n.parent.left = l
	}
	l.right = n
	n.parent = l
}

func (t *rbTree) insertFixup(n *rbNode) {
	for n.parent != nil && n.parent.color == red {
		if n.parent == t.grandparent(n).left {
			u := t.uncle(n)
			if u != nil && u.color == red {
				n.parent.color = black
				u.color = black
				t.grandparent(n).color = red
				n = t.grandparent(n)
			} else {
				if n == n.parent.right {
					n = n.parent
					t.rotateLeft(n)
				}
				n.parent.color = black
				t.grandparent(n).color = red
				t.rotateRight(t.grandparent(n))
			}
		} else {
			u := t.uncle(n)
			if u != nil && u.color == red {
				n.parent.color = black
				u.color = black
				t.grandparent(n).color = red
				n = t.grandparent(n)
			} else {
				if n == n.parent.left {
					n = n.parent
					t.rotateRight(n)
				}
				n.parent.color = black
				t.grandparent(n).color = red
				t.rotateLeft(t.grandparent(n))
			}
		}
	}
	t.root.color = black
}

func (t *rbTree) insert(price int64) *rbNode {
	n := &rbNode{
		price: price,
		color: red,
		level: newPriceLevel(price),
	}

	if t.root == nil {
		t.root = n
		n.color = black
		t.size++
		return n
	}

	cur := t.root
	var parent *rbNode
	for cur != nil {
		parent = cur
		cmp := t.compare(price, cur.price)
		if cmp < 0 {
			cur = cur.left
		} else if cmp > 0 {
			cur = cur.right
		} else {
			return cur
		}
	}

	n.parent = parent
	if t.compare(price, parent.price) < 0 {
		parent.left = n
	} else {
		parent.right = n
	}

	t.insertFixup(n)
	t.size++
	return n
}

func (t *rbTree) find(price int64) *rbNode {
	cur := t.root
	for cur != nil {
		cmp := t.compare(price, cur.price)
		if cmp < 0 {
			cur = cur.left
		} else if cmp > 0 {
			cur = cur.right
		} else {
			return cur
		}
	}
	return nil
}

func (t *rbTree) minimum(n *rbNode) *rbNode {
	if n == nil {
		return nil
	}
	for n.left != nil {
		n = n.left
	}
	return n
}

func (t *rbTree) maximum(n *rbNode) *rbNode {
	if n == nil {
		return nil
	}
	for n.right != nil {
		n = n.right
	}
	return n
}

func (t *rbTree) transplant(u, v *rbNode) {
	if u.parent == nil {
		t.root = v
	} else if u == u.parent.left {
		u.parent.left = v
	} else {
		u.parent.right = v
	}
	if v != nil {
		v.parent = u.parent
	}
}

func (t *rbTree) deleteFixup(n *rbNode) {
	for n != t.root && (n == nil || n.color == black) {
		if n == nil || n.parent == nil {
			break
		}
		if n == n.parent.left {
			s := t.sibling(n)
			if s != nil && s.color == red {
				s.color = black
				n.parent.color = red
				t.rotateLeft(n.parent)
				s = n.parent.right
			}
			if s != nil &&
				(s.left == nil || s.left.color == black) &&
				(s.right == nil || s.right.color == black) {
				s.color = red
				n = n.parent
			} else {
				if s != nil && s.right != nil && s.right.color == black {
					if s.left != nil {
						s.left.color = black
					}
					s.color = red
					t.rotateRight(s)
					s = n.parent.right
				}
				if s != nil {
					s.color = n.parent.color
					if s.right != nil {
						s.right.color = black
					}
				}
				n.parent.color = black
				t.rotateLeft(n.parent)
				n = t.root
			}
		} else {
			s := t.sibling(n)
			if s != nil && s.color == red {
				s.color = black
				n.parent.color = red
				t.rotateRight(n.parent)
				s = n.parent.left
			}
			if s != nil &&
				(s.right == nil || s.right.color == black) &&
				(s.left == nil || s.left.color == black) {
				s.color = red
				n = n.parent
			} else {
				if s != nil && s.left != nil && s.left.color == black {
					if s.right != nil {
						s.right.color = black
					}
					s.color = red
					t.rotateLeft(s)
					s = n.parent.left
				}
				if s != nil {
					s.color = n.parent.color
					if s.left != nil {
						s.left.color = black
					}
				}
				n.parent.color = black
				t.rotateRight(n.parent)
				n = t.root
			}
		}
	}
	if n != nil {
		n.color = black
	}
}

func (t *rbTree) remove(price int64) {
	z := t.find(price)
	if z == nil {
		return
	}

	y := z
	yOriginalColor := y.color
	var x *rbNode

	if z.left == nil {
		x = z.right
		t.transplant(z, z.right)
	} else if z.right == nil {
		x = z.left
		t.transplant(z, z.left)
	} else {
		y = t.minimum(z.right)
		yOriginalColor = y.color
		x = y.right
		if y.parent == z {
			if x != nil {
				x.parent = y
			}
		} else {
			t.transplant(y, y.right)
			y.right = z.right
			y.right.parent = y
		}
		t.transplant(z, y)
		y.left = z.left
		y.left.parent = y
		y.color = z.color
	}

	if yOriginalColor == black {
		t.deleteFixup(x)
	}

	t.size--
}

func (t *rbTree) first() *rbNode {
	return t.minimum(t.root)
}

func (t *rbTree) getLevels(maxLevels int) []*priceLevel {
	levels := make([]*priceLevel, 0, maxLevels)
	if t.root == nil {
		return levels
	}

	var inorder func(n *rbNode, count *int)
	inorder = func(n *rbNode, count *int) {
		if n == nil || *count >= maxLevels {
			return
		}
		inorder(n.left, count)
		if *count < maxLevels {
			levels = append(levels, n.level)
			*count++
		}
		inorder(n.right, count)
	}

	count := 0
	inorder(t.root, &count)
	return levels
}

func NewOrderBook(symbol string) *OrderBook {
	return &OrderBook{
		symbol:   symbol,
		bids:     newRBTree(true),
		asks:     newRBTree(false),
		orderMap: make(map[string]*orderEntry),
	}
}

func (ob *OrderBook) AddOrder(order *types.Order) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	var tree *rbTree
	if order.Side == types.SideBuy {
		tree = ob.bids
	} else {
		tree = ob.asks
	}

	node := tree.insert(order.Price)
	remaining := order.Quantity - order.FilledQty
	node.level.totalQty += remaining
	elem := node.level.orders.PushBack(order)
	ob.orderMap[order.ID] = &orderEntry{
		order: order,
		elem:  elem,
	}
}

func (ob *OrderBook) CancelOrder(orderID string) bool {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	entry, ok := ob.orderMap[orderID]
	if !ok {
		return false
	}

	var tree *rbTree
	if entry.order.Side == types.SideBuy {
		tree = ob.bids
	} else {
		tree = ob.asks
	}

	node := tree.find(entry.order.Price)
	if node == nil {
		delete(ob.orderMap, orderID)
		return false
	}

	remaining := entry.order.Quantity - entry.order.FilledQty
	node.level.totalQty -= remaining
	node.level.orders.Remove(entry.elem)

	if node.level.orders.Len() == 0 {
		tree.remove(entry.order.Price)
	}

	entry.order.Status = types.StatusCancelled
	delete(ob.orderMap, orderID)
	return true
}

func (ob *OrderBook) GetBestBid() (int64, int64) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	node := ob.bids.first()
	if node == nil {
		return 0, 0
	}
	return node.level.price, node.level.totalQty
}

func (ob *OrderBook) GetBestAsk() (int64, int64) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	node := ob.asks.first()
	if node == nil {
		return 0, 0
	}
	return node.level.price, node.level.totalQty
}

func (ob *OrderBook) GetDepth(levels int) types.OrderBook {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	bidLevels := ob.bids.getLevels(levels)
	askLevels := ob.asks.getLevels(levels)

	bids := make([]types.OrderBookLevel, len(bidLevels))
	for i, lvl := range bidLevels {
		bids[i] = types.OrderBookLevel{
			Price:    lvl.price,
			Quantity: lvl.totalQty,
		}
	}

	asks := make([]types.OrderBookLevel, len(askLevels))
	for i, lvl := range askLevels {
		asks[i] = types.OrderBookLevel{
			Price:    lvl.price,
			Quantity: lvl.totalQty,
		}
	}

	return types.OrderBook{
		Symbol:    ob.symbol,
		Bids:      bids,
		Asks:      asks,
		Timestamp: time.Now(),
	}
}

func (ob *OrderBook) Symbol() string {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return ob.symbol
}

func (ob *OrderBook) popFrontOrder(side types.OrderSide) (*types.Order, bool) {
	var tree *rbTree
	if side == types.SideBuy {
		tree = ob.bids
	} else {
		tree = ob.asks
	}

	node := tree.first()
	if node == nil || node.level.orders.Len() == 0 {
		return nil, false
	}

	front := node.level.orders.Front()
	order := front.Value.(*types.Order)
	node.level.orders.Remove(front)
	remaining := order.Quantity - order.FilledQty
	node.level.totalQty -= remaining

	if node.level.orders.Len() == 0 {
		tree.remove(node.level.price)
	}

	delete(ob.orderMap, order.ID)
	return order, true
}

func (ob *OrderBook) peekFrontOrder(side types.OrderSide) (*types.Order, bool) {
	var tree *rbTree
	if side == types.SideBuy {
		tree = ob.bids
	} else {
		tree = ob.asks
	}

	node := tree.first()
	if node == nil || node.level.orders.Len() == 0 {
		return nil, false
	}

	front := node.level.orders.Front()
	return front.Value.(*types.Order), true
}

func (ob *OrderBook) reduceOrderQty(orderID string, fillQty int64) {
	entry, ok := ob.orderMap[orderID]
	if !ok {
		return
	}

	var tree *rbTree
	if entry.order.Side == types.SideBuy {
		tree = ob.bids
	} else {
		tree = ob.asks
	}

	node := tree.find(entry.order.Price)
	if node == nil {
		return
	}

	node.level.totalQty -= fillQty
	entry.order.FilledQty += fillQty
}

func (ob *OrderBook) hasOrder(orderID string) bool {
	_, ok := ob.orderMap[orderID]
	return ok
}
