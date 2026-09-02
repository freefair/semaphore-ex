package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type generatedSSHKeyRepo struct {
	key     db.AccessKey
	created db.AccessKey
	updated db.AccessKey
}

func generatedIntPtr(value int) *int { return &value }

func (r *generatedSSHKeyRepo) GetAccessKey(_ int, _ int) (db.AccessKey, error) {
	if r.key.ID == 0 {
		return db.AccessKey{}, db.ErrNotFound
	}
	return r.key, nil
}

func (r *generatedSSHKeyRepo) GetAccessKeyRefs(int, int) (db.ObjectReferrers, error) {
	return db.ObjectReferrers{}, nil
}

func (r *generatedSSHKeyRepo) GetAccessKeys(int, db.GetAccessKeyOptions, db.RetrieveQueryParams) ([]db.AccessKey, error) {
	return nil, nil
}

func (r *generatedSSHKeyRepo) UpdateAccessKey(key db.AccessKey) error {
	r.updated = key
	r.key = key
	return nil
}

func (r *generatedSSHKeyRepo) CreateAccessKey(key db.AccessKey) (db.AccessKey, error) {
	key.ID = 42
	r.created = key
	r.key = key
	return key, nil
}

func (r *generatedSSHKeyRepo) DeleteAccessKey(int, int) error { return nil }
func (r *generatedSSHKeyRepo) GetTaskAccessKey(int, int) (db.AccessKey, error) {
	return db.AccessKey{}, db.ErrNotFound
}
func (r *generatedSSHKeyRepo) DeleteTaskAccessKeys(int, int) error { return nil }
func (r *generatedSSHKeyRepo) DeleteExpiredTaskAccessKeys() error  { return nil }

func configureGeneratedSSHKeyEncryption(t *testing.T) {
	t.Helper()
	previous := util.Config
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x91}, 32))
	util.Config = &util.ConfigType{AccessKeyEncryption: key}
	t.Cleanup(func() { util.Config = previous })
}

func decryptGeneratedSSHKey(t *testing.T, encryption AccessKeyEncryptionService, key db.AccessKey) db.AccessKey {
	t.Helper()
	key.SshKey = db.SshKey{}
	require.NoError(t, encryption.DeserializeSecret(&key))
	return key
}

func TestCreateGeneratedSSHKeyPersistsOnlyEncryptedPrivateMaterial(t *testing.T) {
	configureGeneratedSSHKeyEncryption(t)
	repo := &generatedSSHKeyRepo{}
	encryption := NewAccessKeyEncryptionService(repo, nil, nil, nil)
	service := NewAccessKeyService(repo, encryption, nil)

	result, err := service.CreateGeneratedSSHKey(CreateGeneratedSSHKeyRequest{
		ProjectID: 1,
		Name:      "deployment",
		Login:     "deploy",
	})
	require.NoError(t, err)
	require.NotNil(t, repo.created.Secret)
	require.True(t, util.Config.AccessKeyEncryptionEnabled())
	require.Contains(t, *repo.created.Secret, ":")
	require.NotContains(t, *repo.created.Secret, "PRIVATE KEY")
	require.Empty(t, repo.created.SshKey.PrivateKey)
	require.Empty(t, repo.created.SshKey.Passphrase)
	require.Empty(t, result.Key.SshKey.PrivateKey)
	require.Empty(t, result.Key.SshKey.Passphrase)
	require.Nil(t, result.Key.Secret)
	require.NotContains(t, result.PublicKey, "PRIVATE")
	require.Contains(t, result.PublicKey, "ssh-ed25519 ")
	require.Contains(t, result.Fingerprint, "SHA256:")

	var metadata GeneratedSSHKeyMetadata
	require.NotNil(t, repo.created.Plain)
	require.NoError(t, json.Unmarshal([]byte(*repo.created.Plain), &metadata))
	require.Equal(t, result.PublicKey, metadata.PublicKey)
	require.Equal(t, result.Fingerprint, metadata.Fingerprint)
	require.Equal(t, GeneratedSSHKeyAlgorithmEd25519, metadata.Algorithm)

	persisted := decryptGeneratedSSHKey(t, encryption, repo.created)
	privateKey, err := ssh.ParseRawPrivateKey([]byte(persisted.SshKey.PrivateKey))
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(result.PublicKey))
	require.NoError(t, err)
	require.Equal(t, publicKey.Marshal(), signer.PublicKey().Marshal())
	require.Equal(t, result.Fingerprint, ssh.FingerprintSHA256(publicKey))
}

func TestParseGeneratedSSHKeyMetadataRejectsUntrustedPlainValues(t *testing.T) {
	pair, err := generateSSHKeyPair(GeneratedSSHKeyAlgorithmEd25519)
	require.NoError(t, err)
	defer pair.clear()
	plain, err := generatedSSHKeyMetadataJSON(pair)
	require.NoError(t, err)

	metadata, ok := ParseGeneratedSSHKeyMetadata(plain)
	require.True(t, ok)
	require.Equal(t, pair.publicKey, metadata.PublicKey)

	untrusted := `{"public_key":"ssh-ed25519 malformed","fingerprint":"SHA256:forged","algorithm":"ed25519"}`
	_, ok = ParseGeneratedSSHKeyMetadata(&untrusted)
	require.False(t, ok)

	algorithmMismatch, err := json.Marshal(GeneratedSSHKeyMetadata{
		PublicKey:   pair.publicKey,
		Fingerprint: pair.fingerprint,
		Algorithm:   GeneratedSSHKeyAlgorithmRSA3072,
	})
	require.NoError(t, err)
	algorithmMismatchPlain := string(algorithmMismatch)
	_, ok = ParseGeneratedSSHKeyMetadata(&algorithmMismatchPlain)
	require.False(t, ok)
}

func TestParseGeneratedSSHKeyMetadataRejectsRSA2048ClaimingRSA3072(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	metadata, err := json.Marshal(GeneratedSSHKeyMetadata{
		PublicKey:   string(ssh.MarshalAuthorizedKey(publicKey)),
		Fingerprint: ssh.FingerprintSHA256(publicKey),
		Algorithm:   GeneratedSSHKeyAlgorithmRSA3072,
	})
	require.NoError(t, err)
	plain := string(metadata)

	_, ok := ParseGeneratedSSHKeyMetadata(&plain)
	require.False(t, ok)
}

func TestGeneratedSSHKeyPairsAuthenticateAgainstRealSSHFixture(t *testing.T) {
	for _, algorithm := range []GeneratedSSHKeyAlgorithm{
		GeneratedSSHKeyAlgorithmEd25519,
		GeneratedSSHKeyAlgorithmRSA3072,
	} {
		t.Run(string(algorithm), func(t *testing.T) {
			pair, err := generateSSHKeyPair(algorithm)
			require.NoError(t, err)
			defer pair.clear()

			privateKey, err := ssh.ParseRawPrivateKey(pair.privatePEM)
			require.NoError(t, err)
			signer, err := ssh.NewSignerFromKey(privateKey)
			require.NoError(t, err)
			publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pair.publicKey))
			require.NoError(t, err)
			require.Equal(t, publicKey.Marshal(), signer.PublicKey().Marshal())
			require.Equal(t, pair.fingerprint, ssh.FingerprintSHA256(publicKey))
			authenticateWithGeneratedSSHKey(t, signer, publicKey)
		})
	}
}

func authenticateWithGeneratedSSHKey(t *testing.T, clientSigner ssh.Signer, allowed ssh.PublicKey) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	require.NoError(t, err)

	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, candidate ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(candidate.Marshal(), allowed.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unexpected SSH public key")
		},
	}
	serverConfig.AddHostKey(hostSigner)
	serverReady := make(chan error, 1)
	closeServer := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverReady <- acceptErr
			return
		}
		defer connection.Close()
		serverConnection, channels, requests, handshakeErr := ssh.NewServerConn(connection, serverConfig)
		if handshakeErr != nil {
			serverReady <- handshakeErr
			return
		}
		go ssh.DiscardRequests(requests)
		go func() {
			for channel := range channels {
				_ = channel.Reject(ssh.Prohibited, "fixture accepts authentication only")
			}
		}()
		serverReady <- nil
		<-closeServer
		_ = serverConnection.Close()
	}()

	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User:            "fixture",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(clientSigner)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // fixture-only host key
	})
	require.NoError(t, err)
	require.NoError(t, <-serverReady)
	require.NoError(t, client.Close())
	close(closeServer)
}

func TestGeneratedSSHKeyCommandsRejectUnsafeInputs(t *testing.T) {
	configureGeneratedSSHKeyEncryption(t)
	repo := &generatedSSHKeyRepo{}
	encryption := NewAccessKeyEncryptionService(repo, nil, nil, nil)
	service := NewAccessKeyService(repo, encryption, nil)

	_, err := service.CreateGeneratedSSHKey(CreateGeneratedSSHKeyRequest{
		ProjectID: 1,
		Name:      "unsupported",
		Algorithm: "rsa-2048",
	})
	require.Error(t, err)
	require.Zero(t, repo.created.ID)

	_, err = service.CreateGeneratedSSHKey(CreateGeneratedSSHKeyRequest{ProjectID: 1})
	require.Error(t, err)
	require.Zero(t, repo.created.ID)

	for _, key := range []db.AccessKey{
		{ID: 1, ProjectID: generatedIntPtr(1), Type: db.AccessKeyString},
		{ID: 1, ProjectID: generatedIntPtr(1), Type: db.AccessKeySSH, Synchronized: true},
		func() db.AccessKey {
			source := db.AccessKeySourceStorageEnv
			return db.AccessKey{ID: 1, ProjectID: generatedIntPtr(1), Type: db.AccessKeySSH, SourceStorageType: &source}
		}(),
	} {
		repo.key = key
		_, err = service.RotateGeneratedSSHKey(RotateGeneratedSSHKeyRequest{
			ProjectID: 1, KeyID: 1, ConfirmRotation: true,
		})
		require.Error(t, err)
		require.Zero(t, repo.updated.ID)
	}

	repo.key = db.AccessKey{ID: 1, ProjectID: generatedIntPtr(1), Type: db.AccessKeySSH}
	_, err = service.RotateGeneratedSSHKey(RotateGeneratedSSHKeyRequest{ProjectID: 1, KeyID: 1})
	require.Error(t, err)
}

func TestGeneratedSSHKeyCommandsFailClosedWithoutEncryption(t *testing.T) {
	previous := util.Config
	util.Config = &util.ConfigType{}
	t.Cleanup(func() { util.Config = previous })
	repo := &generatedSSHKeyRepo{}
	service := NewAccessKeyService(repo, NewAccessKeyEncryptionService(repo, nil, nil, nil), nil)

	_, err := service.CreateGeneratedSSHKey(CreateGeneratedSSHKeyRequest{ProjectID: 1, Name: "key"})
	require.Error(t, err)
	require.Zero(t, repo.created.ID)
}

func TestRotateGeneratedSSHKeyRetainsLoginAndClearsPriorPassphrase(t *testing.T) {
	configureGeneratedSSHKeyEncryption(t)
	repo := &generatedSSHKeyRepo{}
	encryption := NewAccessKeyEncryptionService(repo, nil, nil, nil)
	projectID := 1
	old := db.AccessKey{
		ID: 9, Name: "imported", Type: db.AccessKeySSH, ProjectID: &projectID,
		SshKey: db.SshKey{Login: "deploy", Passphrase: "old-passphrase", PrivateKey: "old-private-key"},
	}
	require.NoError(t, encryption.SerializeSecret(&old))
	old.SshKey = db.SshKey{}
	repo.key = old
	service := NewAccessKeyService(repo, encryption, nil)

	result, err := service.RotateGeneratedSSHKey(RotateGeneratedSSHKeyRequest{
		ProjectID: 1, KeyID: 9, ConfirmRotation: true, Algorithm: GeneratedSSHKeyAlgorithmRSA3072,
	})
	require.NoError(t, err)
	require.Equal(t, GeneratedSSHKeyAlgorithmRSA3072, result.Algorithm)
	require.NotNil(t, repo.updated.Secret)
	require.Empty(t, repo.updated.SshKey.PrivateKey)
	require.Empty(t, repo.updated.SshKey.Passphrase)

	persisted := decryptGeneratedSSHKey(t, encryption, repo.updated)
	require.Equal(t, "deploy", persisted.SshKey.Login)
	require.Empty(t, persisted.SshKey.Passphrase)
	require.NotContains(t, persisted.SshKey.PrivateKey, "old-private-key")
	require.Contains(t, result.PublicKey, "ssh-rsa ")
}

func TestImportedSSHKeyCreateRemainsUnchanged(t *testing.T) {
	configureGeneratedSSHKeyEncryption(t)
	repo := &generatedSSHKeyRepo{}
	encryption := NewAccessKeyEncryptionService(repo, nil, nil, nil)
	service := NewAccessKeyService(repo, encryption, nil)
	projectID := 1

	_, err := service.Create(db.AccessKey{
		Name: "imported", Type: db.AccessKeySSH, ProjectID: &projectID,
		SshKey: db.SshKey{Login: "deploy", PrivateKey: "existing-private-key"},
	})
	require.NoError(t, err)
	persisted := decryptGeneratedSSHKey(t, encryption, repo.created)
	require.Equal(t, "existing-private-key", persisted.SshKey.PrivateKey)
	require.Equal(t, "deploy", persisted.SshKey.Login)
}

func TestGeneratedSSHKeyFingerprintUsesOpenSSHWirePublicKey(t *testing.T) {
	pair, err := generateSSHKeyPair(GeneratedSSHKeyAlgorithmEd25519)
	require.NoError(t, err)
	defer pair.clear()
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pair.publicKey))
	require.NoError(t, err)
	hash := sha256.Sum256(publicKey.Marshal())
	require.Equal(t, "SHA256:"+base64.RawStdEncoding.EncodeToString(hash[:]), pair.fingerprint)
	require.False(t, strings.Contains(pair.publicKey, "BEGIN"))
}
