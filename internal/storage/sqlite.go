package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"trading-bot/pkg/types"
)

var (
	ErrNotFound          = errors.New("record not found")
	ErrEncryptionKey     = errors.New("invalid encryption key")
	ErrEncryptFailed     = errors.New("encryption failed")
	ErrDecryptFailed     = errors.New("decryption failed")
)

const (
	encryptionKeyEnv = "STORAGE_ENCRYPTION_KEY"
)

type SQLiteStore struct {
	db   *sql.DB
	gcm  cipher.AEAD
}

type DailyPnL struct {
	Date          time.Time
	PnL           int64
	EndingBalance int64
	EndingEquity  int64
}

type BalanceLog struct {
	ID          int64
	Type        string
	Amount      int64
	BalanceAfter int64
	Timestamp   time.Time
	Description string
}

type APIKey struct {
	ID        string
	Exchange  string
	APIKey    string
	APISecret string
	CreatedAt time.Time
}

type StrategyConfig struct {
	ID        string
	Name      string
	Type      string
	Symbol    string
	Enabled   bool
	Params    map[string]interface{}
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	store := &SQLiteStore{db: db}

	if err := store.initTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init tables: %w", err)
	}

	if err := store.initEncryption(); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

func (s *SQLiteStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteStore) initTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS strategies (
		id TEXT PRIMARY KEY,
		name TEXT,
		type TEXT,
		symbol TEXT,
		enabled INTEGER,
		params TEXT,
		created_at TEXT,
		updated_at TEXT
	);

	CREATE TABLE IF NOT EXISTS orders (
		id TEXT PRIMARY KEY,
		strategy_id TEXT,
		symbol TEXT,
		type TEXT,
		side TEXT,
		price INTEGER,
		quantity INTEGER,
		filled_qty INTEGER,
		status TEXT,
		stop_price INTEGER,
		created_at TEXT,
		updated_at TEXT,
		exchange_ts INTEGER
	);

	CREATE TABLE IF NOT EXISTS trades (
		id TEXT PRIMARY KEY,
		order_id TEXT,
		symbol TEXT,
		side TEXT,
		price INTEGER,
		quantity INTEGER,
		fee INTEGER,
		timestamp TEXT,
		exchange_ts INTEGER
	);

	CREATE TABLE IF NOT EXISTS positions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		symbol TEXT,
		quantity INTEGER,
		avg_price INTEGER,
		realized_pnl INTEGER,
		snapshot_time TEXT
	);

	CREATE TABLE IF NOT EXISTS daily_pnl (
		date TEXT PRIMARY KEY,
		pnl INTEGER,
		ending_balance INTEGER,
		ending_equity INTEGER
	);

	CREATE TABLE IF NOT EXISTS balance_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT,
		amount INTEGER,
		balance_after INTEGER,
		timestamp TEXT,
		description TEXT
	);

	CREATE TABLE IF NOT EXISTS api_keys (
		id TEXT PRIMARY KEY,
		exchange TEXT,
		api_key_encrypted BLOB,
		api_secret_encrypted BLOB,
		nonce BLOB,
		created_at TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_orders_strategy ON orders(strategy_id);
	CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
	CREATE INDEX IF NOT EXISTS idx_trades_order ON trades(order_id);
	CREATE INDEX IF NOT EXISTS idx_trades_timestamp ON trades(timestamp);
	CREATE INDEX IF NOT EXISTS idx_positions_symbol ON positions(symbol);
	CREATE INDEX IF NOT EXISTS idx_positions_snapshot ON positions(snapshot_time);
	CREATE INDEX IF NOT EXISTS idx_balance_log_timestamp ON balance_log(timestamp);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteStore) initEncryption() error {
	keyHex := os.Getenv(encryptionKeyEnv)
	if keyHex == "" {
		return nil
	}

	key := []byte(keyHex)
	if len(key) != 32 {
		return ErrEncryptionKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEncryptionKey, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEncryptionKey, err)
	}

	s.gcm = gcm
	return nil
}

func (s *SQLiteStore) encrypt(plaintext string) ([]byte, []byte, error) {
	if s.gcm == nil {
		return nil, nil, ErrEncryptionKey
	}

	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrEncryptFailed, err)
	}

	ciphertext := s.gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

func (s *SQLiteStore) decrypt(ciphertext []byte, nonce []byte) (string, error) {
	if s.gcm == nil {
		return "", ErrEncryptionKey
	}

	plaintext, err := s.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecryptFailed, err)
	}

	return string(plaintext), nil
}

func (s *SQLiteStore) SaveStrategy(cfg *StrategyConfig) error {
	if cfg.ID == "" {
		return errors.New("strategy id is required")
	}

	paramsJSON, err := json.Marshal(cfg.Params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	now := time.Now()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = now
	}
	cfg.UpdatedAt = now

	query := `
		INSERT INTO strategies (id, name, type, symbol, enabled, params, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			symbol = excluded.symbol,
			enabled = excluded.enabled,
			params = excluded.params,
			updated_at = excluded.updated_at
	`

	enabled := 0
	if cfg.Enabled {
		enabled = 1
	}

	_, err = s.db.Exec(query,
		cfg.ID,
		cfg.Name,
		cfg.Type,
		cfg.Symbol,
		enabled,
		string(paramsJSON),
		cfg.CreatedAt.Format(time.RFC3339),
		cfg.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

func (s *SQLiteStore) GetStrategy(id string) (*StrategyConfig, error) {
	query := `SELECT id, name, type, symbol, enabled, params, created_at, updated_at FROM strategies WHERE id = ?`

	var (
		name, stype, symbol, paramsStr, createdAtStr, updatedAtStr string
		enabled                                                    int
	)

	err := s.db.QueryRow(query, id).Scan(
		&id, &name, &stype, &symbol, &enabled, &paramsStr, &createdAtStr, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var params map[string]interface{}
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &params); err != nil {
			return nil, fmt.Errorf("unmarshal params: %w", err)
		}
	}

	createdAt, _ := time.Parse(time.RFC3339, createdAtStr)
	updatedAt, _ := time.Parse(time.RFC3339, updatedAtStr)

	return &StrategyConfig{
		ID:        id,
		Name:      name,
		Type:      stype,
		Symbol:    symbol,
		Enabled:   enabled == 1,
		Params:    params,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func (s *SQLiteStore) ListStrategies() ([]*StrategyConfig, error) {
	query := `SELECT id, name, type, symbol, enabled, params, created_at, updated_at FROM strategies ORDER BY created_at DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*StrategyConfig
	for rows.Next() {
		var (
			id, name, stype, symbol, paramsStr, createdAtStr, updatedAtStr string
			enabled                                                        int
		)

		if err := rows.Scan(&id, &name, &stype, &symbol, &enabled, &paramsStr, &createdAtStr, &updatedAtStr); err != nil {
			return nil, err
		}

		var params map[string]interface{}
		if paramsStr != "" {
			if err := json.Unmarshal([]byte(paramsStr), &params); err != nil {
				return nil, fmt.Errorf("unmarshal params: %w", err)
			}
		}

		createdAt, _ := time.Parse(time.RFC3339, createdAtStr)
		updatedAt, _ := time.Parse(time.RFC3339, updatedAtStr)

		result = append(result, &StrategyConfig{
			ID:        id,
			Name:      name,
			Type:      stype,
			Symbol:    symbol,
			Enabled:   enabled == 1,
			Params:    params,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}

	return result, rows.Err()
}

func (s *SQLiteStore) DeleteStrategy(id string) error {
	_, err := s.db.Exec(`DELETE FROM strategies WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) UpdateStrategy(cfg *StrategyConfig) error {
	existing, err := s.GetStrategy(cfg.ID)
	if err != nil {
		return err
	}

	if cfg.Name == "" {
		cfg.Name = existing.Name
	}
	if cfg.Type == "" {
		cfg.Type = existing.Type
	}
	if cfg.Symbol == "" {
		cfg.Symbol = existing.Symbol
	}
	if cfg.Params == nil {
		cfg.Params = existing.Params
	}
	cfg.CreatedAt = existing.CreatedAt

	return s.SaveStrategy(cfg)
}

func (s *SQLiteStore) SaveOrder(order *types.Order) error {
	if order.ID == "" {
		return errors.New("order id is required")
	}

	now := time.Now()
	if order.CreatedAt.IsZero() {
		order.CreatedAt = now
	}
	order.UpdatedAt = now

	query := `
		INSERT INTO orders (id, strategy_id, symbol, type, side, price, quantity, filled_qty, status, stop_price, created_at, updated_at, exchange_ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			strategy_id = excluded.strategy_id,
			symbol = excluded.symbol,
			type = excluded.type,
			side = excluded.side,
			price = excluded.price,
			quantity = excluded.quantity,
			filled_qty = excluded.filled_qty,
			status = excluded.status,
			stop_price = excluded.stop_price,
			updated_at = excluded.updated_at,
			exchange_ts = excluded.exchange_ts
	`

	_, err := s.db.Exec(query,
		order.ID,
		order.StrategyID,
		order.Symbol,
		order.Type.String(),
		order.Side.String(),
		order.Price,
		order.Quantity,
		order.FilledQty,
		order.Status.String(),
		order.StopPrice,
		order.CreatedAt.Format(time.RFC3339),
		order.UpdatedAt.Format(time.RFC3339),
		order.ExchangeTS,
	)
	return err
}

func (s *SQLiteStore) GetOrder(id string) (*types.Order, error) {
	query := `SELECT id, strategy_id, symbol, type, side, price, quantity, filled_qty, status, stop_price, created_at, updated_at, exchange_ts FROM orders WHERE id = ?`

	var (
		strategyID, symbol, orderType, side, status, createdAtStr, updatedAtStr string
		price, quantity, filledQty, stopPrice, exchangeTS                       int64
	)

	err := s.db.QueryRow(query, id).Scan(
		&id, &strategyID, &symbol, &orderType, &side, &price, &quantity, &filledQty, &status, &stopPrice, &createdAtStr, &updatedAtStr, &exchangeTS,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	createdAt, _ := time.Parse(time.RFC3339, createdAtStr)
	updatedAt, _ := time.Parse(time.RFC3339, updatedAtStr)

	return &types.Order{
		ID:         id,
		StrategyID: strategyID,
		Symbol:     symbol,
		Type:       parseOrderType(orderType),
		Side:       parseOrderSide(side),
		Price:      price,
		Quantity:   quantity,
		FilledQty:  filledQty,
		Status:     parseOrderStatus(status),
		StopPrice:  stopPrice,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
		ExchangeTS: exchangeTS,
	}, nil
}

func (s *SQLiteStore) ListOrdersByStrategy(strategyID string) ([]*types.Order, error) {
	query := `SELECT id, strategy_id, symbol, type, side, price, quantity, filled_qty, status, stop_price, created_at, updated_at, exchange_ts FROM orders WHERE strategy_id = ? ORDER BY created_at DESC`
	return s.queryOrders(query, strategyID)
}

func (s *SQLiteStore) ListOrdersByStatus(status types.OrderStatus) ([]*types.Order, error) {
	query := `SELECT id, strategy_id, symbol, type, side, price, quantity, filled_qty, status, stop_price, created_at, updated_at, exchange_ts FROM orders WHERE status = ? ORDER BY created_at DESC`
	return s.queryOrders(query, status.String())
}

func (s *SQLiteStore) queryOrders(query string, args ...interface{}) ([]*types.Order, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*types.Order
	for rows.Next() {
		var (
			id, strategyID, symbol, orderType, side, status, createdAtStr, updatedAtStr string
			price, quantity, filledQty, stopPrice, exchangeTS                       int64
		)

		if err := rows.Scan(&id, &strategyID, &symbol, &orderType, &side, &price, &quantity, &filledQty, &status, &stopPrice, &createdAtStr, &updatedAtStr, &exchangeTS); err != nil {
			return nil, err
		}

		createdAt, _ := time.Parse(time.RFC3339, createdAtStr)
		updatedAt, _ := time.Parse(time.RFC3339, updatedAtStr)

		result = append(result, &types.Order{
			ID:         id,
			StrategyID: strategyID,
			Symbol:     symbol,
			Type:       parseOrderType(orderType),
			Side:       parseOrderSide(side),
			Price:      price,
			Quantity:   quantity,
			FilledQty:  filledQty,
			Status:     parseOrderStatus(status),
			StopPrice:  stopPrice,
			CreatedAt:  createdAt,
			UpdatedAt:  updatedAt,
			ExchangeTS: exchangeTS,
		})
	}

	return result, rows.Err()
}

func (s *SQLiteStore) UpdateOrderStatus(orderID string, status types.OrderStatus) error {
	query := `UPDATE orders SET status = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.Exec(query, status.String(), time.Now().Format(time.RFC3339), orderID)
	return err
}

func (s *SQLiteStore) UpdateOrderFill(orderID string, filledQty int64, status types.OrderStatus) error {
	query := `UPDATE orders SET filled_qty = ?, status = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.Exec(query, filledQty, status.String(), time.Now().Format(time.RFC3339), orderID)
	return err
}

func (s *SQLiteStore) SaveTrade(trade *types.Trade) error {
	if trade.ID == "" {
		return errors.New("trade id is required")
	}

	if trade.Timestamp.IsZero() {
		trade.Timestamp = time.Now()
	}

	query := `
		INSERT INTO trades (id, order_id, symbol, side, price, quantity, fee, timestamp, exchange_ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`

	_, err := s.db.Exec(query,
		trade.ID,
		trade.OrderID,
		trade.Symbol,
		trade.Side.String(),
		trade.Price,
		trade.Quantity,
		trade.Fee,
		trade.Timestamp.Format(time.RFC3339),
		trade.ExchangeTS,
	)
	return err
}

func (s *SQLiteStore) ListTrades() ([]*types.Trade, error) {
	query := `SELECT id, order_id, symbol, side, price, quantity, fee, timestamp, exchange_ts FROM trades ORDER BY timestamp DESC`
	return s.queryTrades(query)
}

func (s *SQLiteStore) ListTradesByOrder(orderID string) ([]*types.Trade, error) {
	query := `SELECT id, order_id, symbol, side, price, quantity, fee, timestamp, exchange_ts FROM trades WHERE order_id = ? ORDER BY timestamp DESC`
	return s.queryTrades(query, orderID)
}

func (s *SQLiteStore) ListTradesByDate(date time.Time) ([]*types.Trade, error) {
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	query := `SELECT id, order_id, symbol, side, price, quantity, fee, timestamp, exchange_ts FROM trades WHERE timestamp >= ? AND timestamp < ? ORDER BY timestamp DESC`
	return s.queryTrades(query, startOfDay.Format(time.RFC3339), endOfDay.Format(time.RFC3339))
}

func (s *SQLiteStore) queryTrades(query string, args ...interface{}) ([]*types.Trade, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*types.Trade
	for rows.Next() {
		var (
			id, orderID, symbol, side, timestampStr string
			price, quantity, fee, exchangeTS        int64
		)

		if err := rows.Scan(&id, &orderID, &symbol, &side, &price, &quantity, &fee, &timestampStr, &exchangeTS); err != nil {
			return nil, err
		}

		timestamp, _ := time.Parse(time.RFC3339, timestampStr)

		result = append(result, &types.Trade{
			ID:         id,
			OrderID:    orderID,
			Symbol:     symbol,
			Side:       parseOrderSide(side),
			Price:      price,
			Quantity:   quantity,
			Fee:        fee,
			Timestamp:  timestamp,
			ExchangeTS: exchangeTS,
		})
	}

	return result, rows.Err()
}

func (s *SQLiteStore) SavePositionSnapshot(pos *types.Position) error {
	snapshotTime := time.Now()
	if !pos.UpdatedAt.IsZero() {
		snapshotTime = pos.UpdatedAt
	}

	query := `INSERT INTO positions (symbol, quantity, avg_price, realized_pnl, snapshot_time) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query,
		pos.Symbol,
		pos.Quantity,
		pos.AvgPrice,
		pos.RealizedPnL,
		snapshotTime.Format(time.RFC3339),
	)
	return err
}

func (s *SQLiteStore) ListPositionSnapshots(symbol string, limit int) ([]*types.Position, error) {
	query := `SELECT symbol, quantity, avg_price, realized_pnl, snapshot_time FROM positions WHERE symbol = ? ORDER BY snapshot_time DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, symbol)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*types.Position
	for rows.Next() {
		var (
			sym, snapshotTimeStr             string
			quantity, avgPrice, realizedPnL int64
		)

		if err := rows.Scan(&sym, &quantity, &avgPrice, &realizedPnL, &snapshotTimeStr); err != nil {
			return nil, err
		}

		snapshotTime, _ := time.Parse(time.RFC3339, snapshotTimeStr)

		result = append(result, &types.Position{
			Symbol:      sym,
			Quantity:    quantity,
			AvgPrice:    avgPrice,
			RealizedPnL: realizedPnL,
			UpdatedAt:   snapshotTime,
		})
	}

	return result, rows.Err()
}

func (s *SQLiteStore) GetLatestPosition(symbol string) (*types.Position, error) {
	query := `SELECT symbol, quantity, avg_price, realized_pnl, snapshot_time FROM positions WHERE symbol = ? ORDER BY snapshot_time DESC LIMIT 1`

	var (
		sym, snapshotTimeStr             string
		quantity, avgPrice, realizedPnL int64
	)

	err := s.db.QueryRow(query, symbol).Scan(&sym, &quantity, &avgPrice, &realizedPnL, &snapshotTimeStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	snapshotTime, _ := time.Parse(time.RFC3339, snapshotTimeStr)

	return &types.Position{
		Symbol:      sym,
		Quantity:    quantity,
		AvgPrice:    avgPrice,
		RealizedPnL: realizedPnL,
		UpdatedAt:   snapshotTime,
	}, nil
}

func (s *SQLiteStore) SaveDailyPnL(dailyPnL *DailyPnL) error {
	query := `
		INSERT INTO daily_pnl (date, pnl, ending_balance, ending_equity)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET
			pnl = excluded.pnl,
			ending_balance = excluded.ending_balance,
			ending_equity = excluded.ending_equity
	`

	dateStr := dailyPnL.Date.Format("2006-01-02")
	_, err := s.db.Exec(query, dateStr, dailyPnL.PnL, dailyPnL.EndingBalance, dailyPnL.EndingEquity)
	return err
}

func (s *SQLiteStore) GetDailyPnL(date time.Time) (*DailyPnL, error) {
	query := `SELECT date, pnl, ending_balance, ending_equity FROM daily_pnl WHERE date = ?`

	dateStr := date.Format("2006-01-02")
	var (
		d                              string
		pnl, endingBalance, endingEquity int64
	)

	err := s.db.QueryRow(query, dateStr).Scan(&d, &pnl, &endingBalance, &endingEquity)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	parsedDate, _ := time.Parse("2006-01-02", d)

	return &DailyPnL{
		Date:          parsedDate,
		PnL:           pnl,
		EndingBalance: endingBalance,
		EndingEquity:  endingEquity,
	}, nil
}

func (s *SQLiteStore) ListDailyPnL(startDate, endDate time.Time) ([]*DailyPnL, error) {
	query := `SELECT date, pnl, ending_balance, ending_equity FROM daily_pnl WHERE date >= ? AND date <= ? ORDER BY date ASC`

	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	rows, err := s.db.Query(query, startStr, endStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*DailyPnL
	for rows.Next() {
		var (
			d                              string
			pnl, endingBalance, endingEquity int64
		)

		if err := rows.Scan(&d, &pnl, &endingBalance, &endingEquity); err != nil {
			return nil, err
		}

		parsedDate, _ := time.Parse("2006-01-02", d)

		result = append(result, &DailyPnL{
			Date:          parsedDate,
			PnL:           pnl,
			EndingBalance: endingBalance,
			EndingEquity:  endingEquity,
		})
	}

	return result, rows.Err()
}

func (s *SQLiteStore) AddBalanceLog(logType string, amount int64, balanceAfter int64, description string) error {
	query := `INSERT INTO balance_log (type, amount, balance_after, timestamp, description) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query,
		logType,
		amount,
		balanceAfter,
		time.Now().Format(time.RFC3339),
		description,
	)
	return err
}

func (s *SQLiteStore) ListBalanceLogs(limit int) ([]*BalanceLog, error) {
	query := `SELECT id, type, amount, balance_after, timestamp, description FROM balance_log ORDER BY timestamp DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*BalanceLog
	for rows.Next() {
		var (
			id               int64
			logType, timestampStr, description string
			amount, balanceAfter int64
		)

		if err := rows.Scan(&id, &logType, &amount, &balanceAfter, &timestampStr, &description); err != nil {
			return nil, err
		}

		timestamp, _ := time.Parse(time.RFC3339, timestampStr)

		result = append(result, &BalanceLog{
			ID:           id,
			Type:         logType,
			Amount:       amount,
			BalanceAfter: balanceAfter,
			Timestamp:    timestamp,
			Description:  description,
		})
	}

	return result, rows.Err()
}

func (s *SQLiteStore) SaveAPIKey(apiKey *APIKey) error {
	if s.gcm == nil {
		return ErrEncryptionKey
	}
	if apiKey.ID == "" {
		return errors.New("api key id is required")
	}

	keyEncrypted, nonce1, err := s.encrypt(apiKey.APIKey)
	if err != nil {
		return err
	}

	secretEncrypted, nonce2, err := s.encrypt(apiKey.APISecret)
	if err != nil {
		return err
	}

	combinedNonce := make([]byte, 0, len(nonce1)+len(nonce2))
	combinedNonce = append(combinedNonce, nonce1...)
	combinedNonce = append(combinedNonce, nonce2...)

	now := time.Now()
	if apiKey.CreatedAt.IsZero() {
		apiKey.CreatedAt = now
	}

	query := `
		INSERT INTO api_keys (id, exchange, api_key_encrypted, api_secret_encrypted, nonce, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			exchange = excluded.exchange,
			api_key_encrypted = excluded.api_key_encrypted,
			api_secret_encrypted = excluded.api_secret_encrypted,
			nonce = excluded.nonce
	`

	_, err = s.db.Exec(query,
		apiKey.ID,
		apiKey.Exchange,
		keyEncrypted,
		secretEncrypted,
		combinedNonce,
		apiKey.CreatedAt.Format(time.RFC3339),
	)
	return err
}

func (s *SQLiteStore) GetAPIKey(id string) (*APIKey, error) {
	if s.gcm == nil {
		return nil, ErrEncryptionKey
	}

	query := `SELECT id, exchange, api_key_encrypted, api_secret_encrypted, nonce, created_at FROM api_keys WHERE id = ?`

	var (
		exchange, createdAtStr           string
		keyEncrypted, secretEncrypted, nonce []byte
	)

	err := s.db.QueryRow(query, id).Scan(
		&id, &exchange, &keyEncrypted, &secretEncrypted, &nonce, &createdAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	nonceSize := s.gcm.NonceSize()
	if len(nonce) < nonceSize*2 {
		return nil, ErrDecryptFailed
	}

	nonce1 := nonce[:nonceSize]
	nonce2 := nonce[nonceSize : nonceSize*2]

	apiKey, err := s.decrypt(keyEncrypted, nonce1)
	if err != nil {
		return nil, err
	}

	apiSecret, err := s.decrypt(secretEncrypted, nonce2)
	if err != nil {
		return nil, err
	}

	createdAt, _ := time.Parse(time.RFC3339, createdAtStr)

	return &APIKey{
		ID:        id,
		Exchange:  exchange,
		APIKey:    apiKey,
		APISecret: apiSecret,
		CreatedAt: createdAt,
	}, nil
}

func (s *SQLiteStore) DeleteAPIKey(id string) error {
	_, err := s.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

func parseOrderType(s string) types.OrderType {
	switch s {
	case "LIMIT":
		return types.OrderTypeLimit
	case "MARKET":
		return types.OrderTypeMarket
	case "STOP_LOSS":
		return types.OrderTypeStopLoss
	case "TAKE_PROFIT":
		return types.OrderTypeTakeProfit
	default:
		return types.OrderTypeLimit
	}
}

func parseOrderSide(s string) types.OrderSide {
	if s == "BUY" {
		return types.SideBuy
	}
	return types.SideSell
}

func parseOrderStatus(s string) types.OrderStatus {
	switch s {
	case "PENDING":
		return types.StatusPending
	case "OPEN":
		return types.StatusOpen
	case "FILLED":
		return types.StatusFilled
	case "PARTIALLY_FILLED":
		return types.StatusPartiallyFilled
	case "CANCELLED":
		return types.StatusCancelled
	case "REJECTED":
		return types.StatusRejected
	case "EXPIRED":
		return types.StatusExpired
	default:
		return types.StatusPending
	}
}
