// SQLite keystore (keys.db) 조회. (TS의 lib/db-keystore 대응)
// 테이블: keys(Name TEXT, KeyID TEXT, EncKey TEXT). readonly 오픈.
package kms

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite 드라이버 (cgo 불필요)
)

// SQLiteKeystore 는 keys.db 핸들.
type SQLiteKeystore struct {
	db *sql.DB
}

// OpenSQLite 는 readonly로 keys.db를 연다.
func OpenSQLite(path string) (*SQLiteKeystore, error) {
	if path == "" {
		return nil, fmt.Errorf("keystore: empty db path")
	}
	// modernc.org/sqlite DSN: file:<path>?mode=ro
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("keystore open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("keystore ping: %w", err)
	}
	return &SQLiteKeystore{db: db}, nil
}

// Lookup 은 Name 행의 KeyID·EncKey를 반환한다.
func (k *SQLiteKeystore) Lookup(name string) (KeyEntry, bool, error) {
	var keyID, encKey string
	err := k.db.QueryRow("SELECT KeyID, EncKey FROM keys WHERE Name = ?", name).Scan(&keyID, &encKey)
	if err == sql.ErrNoRows {
		return KeyEntry{}, false, nil
	}
	if err != nil {
		return KeyEntry{}, false, err
	}
	keyID, encKey = strings.TrimSpace(keyID), strings.TrimSpace(encKey)
	if keyID == "" || encKey == "" {
		return KeyEntry{}, false, nil
	}
	return KeyEntry{KeyID: keyID, CiphertextBase64: encKey}, true, nil
}

// ListByPrefix 는 Name이 prefix로 시작하는 행 이름을 정렬해 반환한다.
func (k *SQLiteKeystore) ListByPrefix(prefix string) ([]string, error) {
	rows, err := k.db.Query("SELECT Name FROM keys WHERE Name LIKE ?", prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, strings.TrimSpace(n))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// Close 는 DB를 닫는다.
func (k *SQLiteKeystore) Close() error {
	return k.db.Close()
}
