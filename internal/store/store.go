package store

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Account struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	SessionID    string    `json:"session_id"`
	ClientCookie string    `json:"client_cookie"`
	ClientUat    string    `json:"client_uat"`
	ProjectID    string    `json:"project_id"`
	UserID       string    `json:"user_id"`
	AgentMode    string    `json:"agent_mode"`
	Email        string    `json:"email"`
	Weight       int       `json:"weight"`
	Enabled      bool      `json:"enabled"`
	RequestCount int64     `json:"request_count"`
	LastUsedAt   time.Time `json:"last_used_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Settings struct {
	ID    int64  `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ApiKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-"`
	KeyFull    string     `json:"key_full,omitempty"`
	KeyPrefix  string     `json:"key_prefix"`
	KeySuffix  string     `json:"key_suffix"`
	Enabled    bool       `json:"enabled"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Store struct {
	db *sql.DB
	mu sync.RWMutex
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return store, nil
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			session_id TEXT NOT NULL,
			client_cookie TEXT NOT NULL,
			client_uat TEXT NOT NULL,
			project_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			agent_mode TEXT DEFAULT 'claude-opus-4.5',
			email TEXT NOT NULL,
			weight INTEGER DEFAULT 1,
			enabled INTEGER DEFAULT 1,
			request_count INTEGER DEFAULT 0,
			last_used_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE NOT NULL,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			key_hash TEXT NOT NULL UNIQUE,
			key_full TEXT NOT NULL DEFAULT '',
			key_prefix TEXT NOT NULL DEFAULT 'sk-',
			key_suffix TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			last_used_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_enabled ON accounts(enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_enabled ON api_keys(enabled)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}

	// 迁移：为现有表添加 key_full 列
	s.db.Exec(`ALTER TABLE api_keys ADD COLUMN key_full TEXT NOT NULL DEFAULT ''`)

	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateAccount(acc *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(`
		INSERT INTO accounts (name, session_id, client_cookie, client_uat, project_id, user_id, agent_mode, email, weight, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, acc.Name, acc.SessionID, acc.ClientCookie, acc.ClientUat, acc.ProjectID, acc.UserID, acc.AgentMode, acc.Email, acc.Weight, acc.Enabled)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	acc.ID = id
	return nil
}

func (s *Store) UpdateAccount(acc *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET
			name = ?, session_id = ?, client_cookie = ?, client_uat = ?,
			project_id = ?, user_id = ?, agent_mode = ?, email = ?,
			weight = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, acc.Name, acc.SessionID, acc.ClientCookie, acc.ClientUat, acc.ProjectID, acc.UserID, acc.AgentMode, acc.Email, acc.Weight, acc.Enabled, acc.ID)
	return err
}

func (s *Store) DeleteAccount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	return err
}

func (s *Store) GetAccount(id int64) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acc := &Account{}
	var lastUsedAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT id, name, session_id, client_cookie, client_uat, project_id, user_id,
			   agent_mode, email, weight, enabled, request_count, last_used_at, created_at, updated_at
		FROM accounts WHERE id = ?
	`, id).Scan(&acc.ID, &acc.Name, &acc.SessionID, &acc.ClientCookie, &acc.ClientUat,
		&acc.ProjectID, &acc.UserID, &acc.AgentMode, &acc.Email, &acc.Weight,
		&acc.Enabled, &acc.RequestCount, &lastUsedAt, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		acc.LastUsedAt = lastUsedAt.Time
	}
	return acc, nil
}

func (s *Store) ListAccounts() ([]*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, name, session_id, client_cookie, client_uat, project_id, user_id,
			   agent_mode, email, weight, enabled, request_count, last_used_at, created_at, updated_at
		FROM accounts ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		acc := &Account{}
		var lastUsedAt sql.NullTime
		err := rows.Scan(&acc.ID, &acc.Name, &acc.SessionID, &acc.ClientCookie, &acc.ClientUat,
			&acc.ProjectID, &acc.UserID, &acc.AgentMode, &acc.Email, &acc.Weight,
			&acc.Enabled, &acc.RequestCount, &lastUsedAt, &acc.CreatedAt, &acc.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if lastUsedAt.Valid {
			acc.LastUsedAt = lastUsedAt.Time
		}
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

func (s *Store) GetEnabledAccounts() ([]*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, name, session_id, client_cookie, client_uat, project_id, user_id,
			   agent_mode, email, weight, enabled, request_count, last_used_at, created_at, updated_at
		FROM accounts WHERE enabled = 1 ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		acc := &Account{}
		var lastUsedAt sql.NullTime
		err := rows.Scan(&acc.ID, &acc.Name, &acc.SessionID, &acc.ClientCookie, &acc.ClientUat,
			&acc.ProjectID, &acc.UserID, &acc.AgentMode, &acc.Email, &acc.Weight,
			&acc.Enabled, &acc.RequestCount, &lastUsedAt, &acc.CreatedAt, &acc.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if lastUsedAt.Valid {
			acc.LastUsedAt = lastUsedAt.Time
		}
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

func (s *Store) IncrementRequestCount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE accounts SET request_count = request_count + 1, last_used_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, id)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var value string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

func (s *Store) CreateApiKey(key *ApiKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(`
		INSERT INTO api_keys (name, key_hash, key_full, key_prefix, key_suffix, enabled)
		VALUES (?, ?, ?, ?, ?, ?)
	`, key.Name, key.KeyHash, key.KeyFull, key.KeyPrefix, key.KeySuffix, key.Enabled)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	key.ID = id

	var createdAt time.Time
	var lastUsedAt sql.NullTime
	if err := s.db.QueryRow(`
		SELECT enabled, last_used_at, created_at
		FROM api_keys WHERE id = ?
	`, id).Scan(&key.Enabled, &lastUsedAt, &createdAt); err != nil {
		return err
	}
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		key.LastUsedAt = &t
	} else {
		key.LastUsedAt = nil
	}
	key.CreatedAt = createdAt

	return nil
}

func (s *Store) ListApiKeys() ([]*ApiKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, name, key_full, key_prefix, key_suffix, enabled, last_used_at, created_at
		FROM api_keys ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*ApiKey
	for rows.Next() {
		key := &ApiKey{}
		var lastUsedAt sql.NullTime
		if err := rows.Scan(&key.ID, &key.Name, &key.KeyFull, &key.KeyPrefix, &key.KeySuffix, &key.Enabled, &lastUsedAt, &key.CreatedAt); err != nil {
			return nil, err
		}
		if lastUsedAt.Valid {
			t := lastUsedAt.Time
			key.LastUsedAt = &t
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}

func (s *Store) GetApiKeyByHash(hash string) (*ApiKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := &ApiKey{}
	var lastUsedAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT id, name, key_hash, key_prefix, key_suffix, enabled, last_used_at, created_at
		FROM api_keys WHERE key_hash = ?
	`, hash).Scan(&key.ID, &key.Name, &key.KeyHash, &key.KeyPrefix, &key.KeySuffix, &key.Enabled, &lastUsedAt, &key.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		key.LastUsedAt = &t
	}
	return key, nil
}

func (s *Store) UpdateApiKeyEnabled(id int64, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(`
		UPDATE api_keys SET enabled = ?
		WHERE id = ?
	`, enabled, id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateApiKeyLastUsed(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, id)
	return err
}

func (s *Store) DeleteApiKey(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec("DELETE FROM api_keys WHERE id = ?", id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetApiKeyByID(id int64) (*ApiKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := &ApiKey{}
	var lastUsedAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT id, name, key_prefix, key_suffix, enabled, last_used_at, created_at
		FROM api_keys WHERE id = ?
	`, id).Scan(&key.ID, &key.Name, &key.KeyPrefix, &key.KeySuffix, &key.Enabled, &lastUsedAt, &key.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		key.LastUsedAt = &t
	}
	return key, nil
}
