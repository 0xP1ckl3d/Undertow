package control

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// One Undertow server database represents an engagement. The vault key lives
// beside that database with owner-only permissions and must be backed up with
// it. A missing key for an existing vault is fatal rather than silently
// replacing a key that would make previously stored secrets unreadable.
func loadCredentialKey(databasePath string, db *sql.DB) ([32]byte, error) {
	var key [32]byte
	path := databasePath + ".credentials.key"
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM credentials").Scan(&count); err != nil {
			return key, err
		}
		if count != 0 {
			return key, errors.New("credential vault key is missing; restore the key paired with this operations database")
		}
		if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
			return key, err
		}
		file, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(createErr, os.ErrExist) {
			data, err = os.ReadFile(path)
		} else if createErr != nil {
			return key, createErr
		} else {
			_, err = file.Write(key[:])
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				_ = os.Remove(path)
				return key, errors.New("write credential vault key")
			}
			return key, nil
		}
	}
	if err != nil || len(data) != len(key) {
		return key, errors.New("credential vault key is unavailable or invalid")
	}
	copy(key[:], data)
	clear(data)
	return key, nil
}

type CredentialRecord struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Domain    string    `json:"domain"`
	Username  string    `json:"username"`
	Kind      string    `json:"kind"`
	OwnerID   string    `json:"owner_id"`
	Shared    bool      `json:"shared"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CredentialInput struct {
	Label    string `json:"label"`
	Domain   string `json:"domain"`
	Username string `json:"username"`
	Kind     string `json:"kind"`
	Secret   string `json:"secret"`
	Shared   bool   `json:"shared"`
}

type credentialMaterial struct {
	CredentialRecord
	Secret string `json:"-"`
}

var errCredentialUnavailable = errors.New("credential unavailable")

func validateCredentialInput(in CredentialInput) error {
	if strings.TrimSpace(in.Label) == "" || len(in.Label) > 128 || strings.ContainsAny(in.Label, "\r\n") || strings.TrimSpace(in.Username) == "" || len(in.Username) > 256 || strings.ContainsAny(in.Username, "\r\n") || len(in.Domain) > 256 || strings.ContainsAny(in.Domain, "\r\n") {
		return errors.New("invalid credential metadata")
	}
	switch in.Kind {
	case "password":
		if in.Secret == "" || len(in.Secret) > 512 {
			return errors.New("password must be 1–512 bytes")
		}
	case "nt_hash":
		if len(in.Secret) != 32 {
			return errors.New("NT hash must be 32 hexadecimal characters")
		}
		if _, err := hex.DecodeString(in.Secret); err != nil {
			return errors.New("NT hash must be 32 hexadecimal characters")
		}
	default:
		return errors.New("credential kind must be password or nt_hash")
	}
	return nil
}

func credentialAAD(id, owner, kind string) []byte {
	return []byte("undertow.credentials.v1\x00" + id + "\x00" + owner + "\x00" + kind)
}

func (s *OperationsStore) encryptCredential(id, owner, kind, secret string) ([]byte, error) {
	block, err := aes.NewCipher(s.credentialKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, aead.Seal(nil, nonce, []byte(secret), credentialAAD(id, owner, kind))...), nil
}

func (s *OperationsStore) decryptCredential(id, owner, kind string, sealed []byte) (string, error) {
	block, err := aes.NewCipher(s.credentialKey[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		return "", errors.New("credential vault entry is damaged")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], credentialAAD(id, owner, kind))
	if err != nil {
		return "", errors.New("credential vault entry cannot be decrypted")
	}
	defer clear(plain)
	return string(plain), nil
}

func (s *OperationsStore) CreateCredential(owner string, in CredentialInput) (CredentialRecord, error) {
	if err := validateCredentialInput(in); err != nil {
		return CredentialRecord{}, err
	}
	if owner == "" {
		return CredentialRecord{}, errors.New("authenticated operator required")
	}
	var random [16]byte
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return CredentialRecord{}, err
	}
	now := time.Now().UTC()
	record := CredentialRecord{ID: hex.EncodeToString(random[:]), Label: strings.TrimSpace(in.Label), Domain: in.Domain, Username: strings.TrimSpace(in.Username), Kind: in.Kind, OwnerID: owner, Shared: in.Shared, CreatedAt: now, UpdatedAt: now}
	sealed, err := s.encryptCredential(record.ID, owner, record.Kind, in.Secret)
	if err != nil {
		return CredentialRecord{}, err
	}
	_, err = s.db.Exec(`INSERT INTO credentials(id,label,domain,username,kind,ciphertext,owner_id,shared,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, record.ID, record.Label, record.Domain, record.Username, record.Kind, sealed, record.OwnerID, record.Shared, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return CredentialRecord{}, errors.New("store credential failed")
	}
	return record, nil
}

func scanCredential(rows interface{ Scan(...any) error }) (CredentialRecord, error) {
	var record CredentialRecord
	var shared int
	var created, updated string
	err := rows.Scan(&record.ID, &record.Label, &record.Domain, &record.Username, &record.Kind, &record.OwnerID, &shared, &created, &updated)
	if err != nil {
		return record, err
	}
	record.Shared = shared != 0
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return record, nil
}

func (s *OperationsStore) ListCredentials(operatorID, role string) ([]CredentialRecord, error) {
	var rows *sql.Rows
	var err error
	query := `SELECT id,label,domain,username,kind,owner_id,shared,created_at,updated_at FROM credentials`
	if role == TeamLeaderRole {
		rows, err = s.db.Query(query + ` ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.Query(query+` WHERE owner_id=? OR shared=1 ORDER BY created_at DESC`, operatorID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CredentialRecord{}
	for rows.Next() {
		record, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, record)
	}
	return items, rows.Err()
}

func (s *OperationsStore) ResolveCredential(id, operatorID, role string) (credentialMaterial, error) {
	var material credentialMaterial
	var shared int
	var sealed []byte
	var created, updated string
	err := s.db.QueryRow(`SELECT id,label,domain,username,kind,owner_id,shared,created_at,updated_at,ciphertext FROM credentials WHERE id=?`, id).Scan(&material.ID, &material.Label, &material.Domain, &material.Username, &material.Kind, &material.OwnerID, &shared, &created, &updated, &sealed)
	if err != nil || operatorID == "" || material.OwnerID != operatorID && shared == 0 && role != TeamLeaderRole {
		return credentialMaterial{}, errors.New("credential unavailable")
	}
	material.Shared = shared != 0
	material.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	material.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	material.Secret, err = s.decryptCredential(id, material.OwnerID, material.Kind, sealed)
	clear(sealed)
	if err != nil {
		return credentialMaterial{}, err
	}
	return material, nil
}

func (s *OperationsStore) ReplaceCredential(id, operatorID, role string, in CredentialInput) (CredentialRecord, error) {
	if err := validateCredentialInput(in); err != nil {
		return CredentialRecord{}, err
	}
	var owner, created string
	if err := s.db.QueryRow(`SELECT owner_id,created_at FROM credentials WHERE id=?`, id).Scan(&owner, &created); err != nil || owner != operatorID && role != TeamLeaderRole {
		return CredentialRecord{}, errors.New("credential unavailable")
	}
	sealed, err := s.encryptCredential(id, owner, in.Kind, in.Secret)
	if err != nil {
		return CredentialRecord{}, err
	}
	now := time.Now().UTC()
	_, err = s.db.Exec(`UPDATE credentials SET label=?,domain=?,username=?,kind=?,ciphertext=?,shared=?,updated_at=? WHERE id=?`, strings.TrimSpace(in.Label), in.Domain, strings.TrimSpace(in.Username), in.Kind, sealed, in.Shared, now.Format(time.RFC3339Nano), id)
	if err != nil {
		return CredentialRecord{}, errors.New("update credential failed")
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, created)
	return CredentialRecord{ID: id, Label: strings.TrimSpace(in.Label), Domain: in.Domain, Username: strings.TrimSpace(in.Username), Kind: in.Kind, OwnerID: owner, Shared: in.Shared, CreatedAt: createdAt, UpdatedAt: now}, nil
}

func (s *OperationsStore) DeleteCredential(id, operatorID, role string) error {
	result, err := s.db.Exec(`DELETE FROM credentials WHERE id=? AND (owner_id=? OR ?=?)`, id, operatorID, role, TeamLeaderRole)
	if err != nil {
		return errors.New("delete credential failed")
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errors.New("credential unavailable")
	}
	return nil
}

func (r CredentialRecord) Account() string {
	if r.Domain == "" {
		return r.Username
	}
	return fmt.Sprintf("%s\\%s", r.Domain, r.Username)
}
