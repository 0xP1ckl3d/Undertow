package control

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	OperatorRole   = "operator"
	TeamLeaderRole = "team_leader"
)

var operatorIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,63}$`)

type OperatorAccount struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	Revoked     bool   `json:"revoked"`
	Version     int64  `json:"-"`
}

func validOperator(id, display, password, role string) error {
	if !operatorIDPattern.MatchString(id) {
		return errors.New("operator ID must be 3-64 lowercase letters, digits, dot, underscore or hyphen, starting with a letter")
	}
	if display == "" || len(display) > 128 || strings.ContainsAny(display, "\r\n\x00") {
		return errors.New("display name must be 1-128 characters on one line")
	}
	if role != OperatorRole && role != TeamLeaderRole {
		return errors.New("invalid operator role")
	}
	if len(password) < 12 || len(password) > 72 {
		return errors.New("operator password must be 12-72 bytes")
	}
	return nil
}

func (s *OperationsStore) OperatorCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM operator_accounts`).Scan(&count)
	return count, err
}

// BootstrapOperator succeeds once, against an empty store. It is called by a
// local, offline server command; the connection handshake cannot bootstrap.
func (s *OperationsStore) BootstrapOperator(id, display, password string) error {
	if err := validOperator(id, display, password, TeamLeaderRole); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`INSERT INTO operator_accounts(id,display_name,role,password_hash) SELECT ?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM operator_accounts)`, id, display, TeamLeaderRole, hash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("operator bootstrap is closed: accounts already exist")
	}
	return nil
}

func (s *OperationsStore) AuthenticateOperator(id, password string) (OperatorAccount, error) {
	var a OperatorAccount
	var hash []byte
	var disabled int
	err := s.db.QueryRow(`SELECT id,display_name,role,password_hash,disabled,version FROM operator_accounts WHERE id=?`, id).Scan(&a.ID, &a.DisplayName, &a.Role, &hash, &disabled, &a.Version)
	if err != nil || disabled != 0 || bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		return OperatorAccount{}, errors.New("invalid operator credentials")
	}
	return a, nil
}

func (s *OperationsStore) Operator(id string) (OperatorAccount, error) {
	var a OperatorAccount
	var disabled int
	err := s.db.QueryRow(`SELECT id,display_name,role,disabled,version FROM operator_accounts WHERE id=?`, id).Scan(&a.ID, &a.DisplayName, &a.Role, &disabled, &a.Version)
	a.Disabled = disabled != 0
	a.Revoked = disabled == 2
	return a, err
}

func (s *OperationsStore) ListOperators() ([]OperatorAccount, error) {
	rows, err := s.db.Query(`SELECT id,display_name,role,disabled,version FROM operator_accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperatorAccount{}
	for rows.Next() {
		var a OperatorAccount
		var disabled int
		if err := rows.Scan(&a.ID, &a.DisplayName, &a.Role, &disabled, &a.Version); err != nil {
			return nil, err
		}
		a.Disabled = disabled != 0
		a.Revoked = disabled == 2
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *OperationsStore) CreateOperator(id, display, password, role string) error {
	if err := validOperator(id, display, password, role); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO operator_accounts(id,display_name,role,password_hash) VALUES(?,?,?,?)`, id, display, role, hash)
	return err
}

// UpdateOperator uses a transaction so the final active Team Leader cannot be
// disabled or demoted, even when multiple management requests arrive together.
func (s *OperationsStore) UpdateOperator(id string, role *string, disabled *bool, password *string) error {
	if role != nil && *role != OperatorRole && *role != TeamLeaderRole {
		return errors.New("invalid operator role")
	}
	var hash []byte
	var err error
	if password != nil {
		if len(*password) < 12 || len(*password) > 72 {
			return errors.New("operator password must be 12-72 bytes")
		}
		hash, err = bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldRole string
	var oldDisabled int
	var oldHash []byte
	var version int64
	if err := tx.QueryRow(`SELECT role,disabled,password_hash,version FROM operator_accounts WHERE id=?`, id).Scan(&oldRole, &oldDisabled, &oldHash, &version); err != nil {
		return err
	}
	if oldDisabled == 2 {
		return errors.New("revoked accounts cannot be changed or enabled")
	}
	newRole, newDisabled := oldRole, oldDisabled
	if role != nil {
		newRole = *role
	}
	if disabled != nil {
		if *disabled {
			newDisabled = 1
		} else {
			newDisabled = 0
		}
	}
	if oldRole == TeamLeaderRole && oldDisabled == 0 && (newRole != TeamLeaderRole || newDisabled != 0) {
		var leaders int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM operator_accounts WHERE role=? AND disabled=0`, TeamLeaderRole).Scan(&leaders); err != nil {
			return err
		}
		if leaders <= 1 {
			return errors.New("cannot remove the final active Team Leader")
		}
	}
	if hash == nil {
		hash = oldHash
	}
	_, err = tx.Exec(`UPDATE operator_accounts SET role=?,disabled=?,password_hash=?,version=? WHERE id=?`, newRole, newDisabled, hash, version+1, id)
	if err != nil {
		return fmt.Errorf("update operator: %w", err)
	}
	return tx.Commit()
}

func (s *OperationsStore) DeleteOperator(id string) error {
	// Revocation is permanent, retaining the account and its audit identity.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	var disabled int
	if err := tx.QueryRow(`SELECT role,disabled FROM operator_accounts WHERE id=?`, id).Scan(&role, &disabled); err != nil {
		return err
	}
	if role == TeamLeaderRole && disabled == 0 {
		var leaders int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM operator_accounts WHERE role=? AND disabled=0`, TeamLeaderRole).Scan(&leaders); err != nil {
			return err
		}
		if leaders <= 1 {
			return errors.New("cannot remove the final active Team Leader")
		}
	}
	if _, err := tx.Exec(`UPDATE operator_accounts SET disabled=2,version=version+1 WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
