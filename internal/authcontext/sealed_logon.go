package authcontext

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// CreationKey is a public, single-use agent key for a live creation request.
// The server forwards only public keys and sealed ciphertext, never logon data.
type CreationKey struct {
	ID        string    `json:"id"`
	PublicKey []byte    `json:"public_key"`
	ExpiresAt time.Time `json:"expires_at"`
}
type SealedLogon struct {
	KeyID      string `json:"key_id"`
	PublicKey  []byte `json:"public_key"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}
type creationPrivateKey struct {
	key     *ecdh.PrivateKey
	expires time.Time
}
type CreationKeys struct {
	mu   sync.Mutex
	keys map[string]creationPrivateKey
}

func (k *CreationKeys) expireLocked() {
	for id, key := range k.keys {
		if !time.Now().Before(key.expires) {
			delete(k.keys, id)
		}
	}
}
func (k *CreationKeys) Issue() (CreationKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.expireLocked()
	if len(k.keys) >= Limit {
		return CreationKey{}, errors.New("live token creation limit reached")
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return CreationKey{}, err
	}
	id, err := opaqueID()
	if err != nil {
		return CreationKey{}, err
	}
	if k.keys == nil {
		k.keys = map[string]creationPrivateKey{}
	}
	expires := time.Now().UTC().Add(time.Minute)
	k.keys[id] = creationPrivateKey{private, expires}
	time.AfterFunc(time.Minute, func() { k.mu.Lock(); defer k.mu.Unlock(); k.expireLocked() })
	return CreationKey{ID: id, PublicKey: private.PublicKey().Bytes(), ExpiresAt: expires}, nil
}
func logonCipher(shared []byte) (cipher.AEAD, error) {
	defer clear(shared)
	key := sha256.Sum256(append([]byte("undertow.token-logon.v1\x00"), shared...))
	block, err := aes.NewCipher(key[:])
	clear(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SealLogon runs only in the operator client. No plaintext credential material
// is forwarded through server APIs, Jobs, audit, or durable queues.
func SealLogon(key CreationKey, r LogonRequest) (SealedLogon, error) {
	if !ValidID(key.ID) || !time.Now().Before(key.ExpiresAt) {
		return SealedLogon{}, errors.New("token creation key expired; retry creation")
	}
	public, err := ecdh.X25519().NewPublicKey(key.PublicKey)
	if err != nil {
		return SealedLogon{}, errors.New("invalid agent creation key")
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return SealedLogon{}, err
	}
	shared, err := private.ECDH(public)
	if err != nil {
		return SealedLogon{}, err
	}
	aead, err := logonCipher(shared)
	if err != nil {
		return SealedLogon{}, err
	}
	plaintext, err := json.Marshal(r)
	if err != nil {
		return SealedLogon{}, err
	}
	defer clear(plaintext)
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return SealedLogon{}, err
	}
	return SealedLogon{KeyID: key.ID, PublicKey: private.PublicKey().Bytes(), Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plaintext, []byte(key.ID))}, nil
}
func (k *CreationKeys) Open(sealed SealedLogon) (LogonRequest, error) {
	k.mu.Lock()
	k.expireLocked()
	key, ok := k.keys[sealed.KeyID]
	delete(k.keys, sealed.KeyID)
	k.mu.Unlock()
	if !ok {
		return LogonRequest{}, errors.New("token creation key expired or already used; retry creation")
	}
	public, err := ecdh.X25519().NewPublicKey(sealed.PublicKey)
	if err != nil {
		return LogonRequest{}, errors.New("invalid sealed logon")
	}
	shared, err := key.key.ECDH(public)
	if err != nil {
		return LogonRequest{}, errors.New("invalid sealed logon")
	}
	aead, err := logonCipher(shared)
	if err != nil {
		return LogonRequest{}, err
	}
	if len(sealed.Nonce) != aead.NonceSize() || len(sealed.Ciphertext) > 4096 {
		return LogonRequest{}, errors.New("invalid sealed logon")
	}
	plaintext, err := aead.Open(nil, sealed.Nonce, sealed.Ciphertext, []byte(sealed.KeyID))
	if err != nil {
		return LogonRequest{}, errors.New("sealed logon authentication failed")
	}
	defer clear(plaintext)
	var r LogonRequest
	if json.Unmarshal(plaintext, &r) != nil {
		return LogonRequest{}, errors.New("invalid sealed logon")
	}
	return r, nil
}
func (k *CreationKeys) Clear() { k.mu.Lock(); defer k.mu.Unlock(); clear(k.keys) }
