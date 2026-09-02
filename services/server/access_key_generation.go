package server

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/util"
	"golang.org/x/crypto/ssh"
)

type GeneratedSSHKeyAlgorithm string

const (
	GeneratedSSHKeyAlgorithmEd25519 GeneratedSSHKeyAlgorithm = "ed25519"
	GeneratedSSHKeyAlgorithmRSA3072 GeneratedSSHKeyAlgorithm = "rsa-3072"
)

// CreateGeneratedSSHKeyRequest intentionally contains no secret or storage
// fields. Server-generated SSH keys are always local access keys.
type CreateGeneratedSSHKeyRequest struct {
	ProjectID int
	Name      string
	Login     string
	Algorithm GeneratedSSHKeyAlgorithm
}

// RotateGeneratedSSHKeyRequest requires an affirmative confirmation so callers
// cannot replace a referenced key through an ordinary metadata update.
type RotateGeneratedSSHKeyRequest struct {
	ProjectID       int
	KeyID           int
	Algorithm       GeneratedSSHKeyAlgorithm
	ConfirmRotation bool
}

// GeneratedSSHKeyResult contains only material which is safe to return to a
// caller. The access key is scrubbed before it leaves the service layer.
type GeneratedSSHKeyResult struct {
	Key         db.AccessKey
	PublicKey   string
	Fingerprint string
	Algorithm   GeneratedSSHKeyAlgorithm
}

// GeneratedSSHKeyMetadata is the non-sensitive metadata retained for a
// server-generated SSH key. It is safe to expose in access-key read responses.
type GeneratedSSHKeyMetadata struct {
	PublicKey   string                   `json:"public_key"`
	Fingerprint string                   `json:"fingerprint"`
	Algorithm   GeneratedSSHKeyAlgorithm `json:"algorithm"`
}

type generatedSSHKeyPair struct {
	privatePEM  []byte
	publicKey   string
	fingerprint string
	algorithm   GeneratedSSHKeyAlgorithm
}

func (pair *generatedSSHKeyPair) clear() {
	clear(pair.privatePEM)
	pair.privatePEM = nil
}

func normalizeGeneratedSSHKeyAlgorithm(algorithm GeneratedSSHKeyAlgorithm) (GeneratedSSHKeyAlgorithm, error) {
	if algorithm == "" {
		return GeneratedSSHKeyAlgorithmEd25519, nil
	}

	switch algorithm {
	case GeneratedSSHKeyAlgorithmEd25519, GeneratedSSHKeyAlgorithmRSA3072:
		return algorithm, nil
	default:
		return "", common_errors.NewUserErrorS("unsupported generated SSH key algorithm")
	}
}

// ValidateGeneratedSSHKeyAlgorithm checks an untrusted command parameter without
// generating or retaining any key material.
func ValidateGeneratedSSHKeyAlgorithm(algorithm GeneratedSSHKeyAlgorithm) error {
	_, err := normalizeGeneratedSSHKeyAlgorithm(algorithm)
	return err
}

func generateSSHKeyPair(algorithm GeneratedSSHKeyAlgorithm) (generatedSSHKeyPair, error) {
	algorithm, err := normalizeGeneratedSSHKeyAlgorithm(algorithm)
	if err != nil {
		return generatedSSHKeyPair{}, err
	}

	var privateKey crypto.Signer
	switch algorithm {
	case GeneratedSSHKeyAlgorithmEd25519:
		_, generated, generateErr := ed25519.GenerateKey(rand.Reader)
		if generateErr != nil {
			return generatedSSHKeyPair{}, fmt.Errorf("generate Ed25519 SSH key: %w", generateErr)
		}
		privateKey = generated
	case GeneratedSSHKeyAlgorithmRSA3072:
		generated, generateErr := rsa.GenerateKey(rand.Reader, 3072)
		if generateErr != nil {
			return generatedSSHKeyPair{}, fmt.Errorf("generate RSA SSH key: %w", generateErr)
		}
		privateKey = generated
	default:
		return generatedSSHKeyPair{}, common_errors.NewUserErrorS("unsupported generated SSH key algorithm")
	}

	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("encode generated SSH public key: %w", err)
	}

	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("encode generated SSH private key: %w", err)
	}
	defer clear(privateDER)

	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	if len(privatePEM) == 0 {
		return generatedSSHKeyPair{}, fmt.Errorf("encode generated SSH private key")
	}

	return generatedSSHKeyPair{
		privatePEM:  privatePEM,
		publicKey:   string(ssh.MarshalAuthorizedKey(publicKey)),
		fingerprint: ssh.FingerprintSHA256(publicKey),
		algorithm:   algorithm,
	}, nil
}

func generatedSSHKeyMetadataJSON(pair generatedSSHKeyPair) (*string, error) {
	metadata, err := json.Marshal(GeneratedSSHKeyMetadata{
		PublicKey:   pair.publicKey,
		Fingerprint: pair.fingerprint,
		Algorithm:   pair.algorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("encode generated SSH key metadata: %w", err)
	}
	value := string(metadata)
	return &value, nil
}

// ParseGeneratedSSHKeyMetadata recognizes only metadata produced by this
// service. Arbitrary legacy Plain values remain opaque to callers.
func ParseGeneratedSSHKeyMetadata(plain *string) (*GeneratedSSHKeyMetadata, bool) {
	if plain == nil {
		return nil, false
	}

	var metadata GeneratedSSHKeyMetadata
	if err := json.Unmarshal([]byte(*plain), &metadata); err != nil || metadata.Algorithm == "" {
		return nil, false
	}
	algorithm, err := normalizeGeneratedSSHKeyAlgorithm(metadata.Algorithm)
	if err != nil || algorithm != metadata.Algorithm {
		return nil, false
	}
	publicKey, remainder, _, _, err := ssh.ParseAuthorizedKey([]byte(metadata.PublicKey))
	if err != nil || len(remainder) != 0 || metadata.Fingerprint != ssh.FingerprintSHA256(publicKey) {
		return nil, false
	}
	if metadata.Algorithm == GeneratedSSHKeyAlgorithmEd25519 && publicKey.Type() != ssh.KeyAlgoED25519 {
		return nil, false
	}
	if metadata.Algorithm == GeneratedSSHKeyAlgorithmRSA3072 && publicKey.Type() != ssh.KeyAlgoRSA {
		return nil, false
	}
	if metadata.Algorithm == GeneratedSSHKeyAlgorithmRSA3072 {
		cryptoPublicKey, ok := publicKey.(ssh.CryptoPublicKey)
		if !ok {
			return nil, false
		}
		rsaPublicKey, ok := cryptoPublicKey.CryptoPublicKey().(*rsa.PublicKey)
		if !ok || rsaPublicKey.N.BitLen() != 3072 {
			return nil, false
		}
	}

	return &metadata, true
}

func ensureGeneratedSSHKeyEncryption() error {
	if util.Config == nil || !util.Config.AccessKeyEncryptionEnabled() {
		return common_errors.NewUserErrorS("generated SSH keys require access-key encryption")
	}
	return nil
}

func generatedSSHKeyResult(key db.AccessKey, pair generatedSSHKeyPair) GeneratedSSHKeyResult {
	key.Secret = nil
	key.SshKey.PrivateKey = ""
	key.SshKey.Passphrase = ""
	return GeneratedSSHKeyResult{
		Key:         key,
		PublicKey:   pair.publicKey,
		Fingerprint: pair.fingerprint,
		Algorithm:   pair.algorithm,
	}
}

func (s *AccessKeyServiceImpl) persistGeneratedSSHKey(key db.AccessKey, algorithm GeneratedSSHKeyAlgorithm, rotate bool) (result GeneratedSSHKeyResult, err error) {
	pair, err := generateSSHKeyPair(algorithm)
	if err != nil {
		return GeneratedSSHKeyResult{}, err
	}
	defer pair.clear()

	metadata, err := generatedSSHKeyMetadataJSON(pair)
	if err != nil {
		return GeneratedSSHKeyResult{}, err
	}

	key.Type = db.AccessKeySSH
	key.Plain = metadata
	key.IgnorePlain = false
	key.OverrideSecret = rotate
	key.SshKey.Passphrase = ""
	key.SshKey.PrivateKey = string(pair.privatePEM)
	defer func() {
		key.SshKey.PrivateKey = ""
		key.SshKey.Passphrase = ""
	}()

	if err = s.encryptionService.SerializeSecret(&key); err != nil {
		return GeneratedSSHKeyResult{}, err
	}

	key.SshKey.PrivateKey = ""
	key.SshKey.Passphrase = ""
	if rotate {
		err = s.accessKeyRepo.UpdateAccessKey(key)
		if err != nil {
			return GeneratedSSHKeyResult{}, err
		}
		return generatedSSHKeyResult(key, pair), nil
	}

	created, err := s.accessKeyRepo.CreateAccessKey(key)
	if err != nil {
		return GeneratedSSHKeyResult{}, err
	}
	return generatedSSHKeyResult(created, pair), nil
}

// CreateGeneratedSSHKey creates a local, encrypted SSH access key. Existing
// imported-key create requests continue to use Create unchanged.
func (s *AccessKeyServiceImpl) CreateGeneratedSSHKey(request CreateGeneratedSSHKeyRequest) (GeneratedSSHKeyResult, error) {
	if request.ProjectID <= 0 {
		return GeneratedSSHKeyResult{}, common_errors.NewUserErrorS("project id is required")
	}
	if request.Name == "" {
		return GeneratedSSHKeyResult{}, common_errors.NewUserErrorS("name can not be empty")
	}
	if err := ensureGeneratedSSHKeyEncryption(); err != nil {
		return GeneratedSSHKeyResult{}, err
	}

	projectID := request.ProjectID
	return s.persistGeneratedSSHKey(db.AccessKey{
		Name:      request.Name,
		Type:      db.AccessKeySSH,
		ProjectID: &projectID,
		SshKey: db.SshKey{
			Login: request.Login,
		},
	}, request.Algorithm, false)
}

// RotateGeneratedSSHKey replaces a local SSH private key only after an explicit
// confirmation. It retains the existing login but deliberately clears any old
// private-key passphrase because generated keys are stored unencrypted inside
// the encrypted access-key envelope.
func (s *AccessKeyServiceImpl) RotateGeneratedSSHKey(request RotateGeneratedSSHKeyRequest) (GeneratedSSHKeyResult, error) {
	if request.ProjectID <= 0 || request.KeyID <= 0 {
		return GeneratedSSHKeyResult{}, common_errors.NewUserErrorS("project id and access key id are required")
	}
	if !request.ConfirmRotation {
		return GeneratedSSHKeyResult{}, common_errors.NewUserErrorS("SSH key rotation requires confirmation")
	}
	if err := ensureGeneratedSSHKeyEncryption(); err != nil {
		return GeneratedSSHKeyResult{}, err
	}

	key, err := s.accessKeyRepo.GetAccessKey(request.ProjectID, request.KeyID)
	if err != nil {
		return GeneratedSSHKeyResult{}, err
	}
	if key.Type != db.AccessKeySSH {
		return GeneratedSSHKeyResult{}, common_errors.NewUserErrorS("only SSH access keys can be rotated")
	}
	if key.Synchronized {
		return GeneratedSSHKeyResult{}, common_errors.NewUserErrorS("synchronized SSH access keys cannot be rotated")
	}
	if key.SourceStorageType != nil {
		return GeneratedSSHKeyResult{}, common_errors.NewUserErrorS("generated SSH keys require local storage")
	}

	if err = s.encryptionService.DeserializeSecret(&key); err != nil {
		return GeneratedSSHKeyResult{}, err
	}
	login := key.SshKey.Login
	key.SshKey = db.SshKey{Login: login}

	return s.persistGeneratedSSHKey(key, request.Algorithm, true)
}
