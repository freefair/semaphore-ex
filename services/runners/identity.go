package runners

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

func runnerRegistrationRequiresIdentity(token string) bool {
	return strings.HasPrefix(token, db.RunnerSecureRegistrationTokenPrefix)
}

// EnsureRunnerIdentity loads or creates the runner's private Ed25519 identity.
// Only its public half is sent to the server; the private key remains in a 0600 file.
func EnsureRunnerIdentity(configFilePath *string) error {
	if util.Config.Runner.IdentityPublicKey != "" {
		return nil
	}
	identityPath := util.Config.Runner.IdentityPrivateKeyFile
	if identityPath == "" {
		if configFilePath == nil || *configFilePath == "" {
			return fmt.Errorf("runner identity requires a config file path or identity_private_key_file")
		}
		identityPath = *configFilePath + ".runner-identity"
		util.Config.Runner.IdentityPrivateKeyFile = identityPath
	}

	encoded, err := os.ReadFile(identityPath)
	if os.IsNotExist(err) {
		publicKey, privateKey, generateErr := ed25519.GenerateKey(rand.Reader)
		if generateErr != nil {
			return fmt.Errorf("generate runner identity: %w", generateErr)
		}
		encoded = []byte(base64.RawStdEncoding.EncodeToString(privateKey))
		file, openErr := os.OpenFile(identityPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if openErr != nil {
			return fmt.Errorf("create runner identity: %w", openErr)
		}
		if _, writeErr := file.Write(encoded); writeErr != nil {
			_ = file.Close()
			return fmt.Errorf("write runner identity: %w", writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close runner identity: %w", closeErr)
		}
		util.Config.Runner.IdentityPublicKey = base64.RawStdEncoding.EncodeToString(publicKey)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read runner identity: %w", err)
	}
	privateKey, err := base64.RawStdEncoding.DecodeString(string(encoded))
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("runner identity file does not contain an Ed25519 private key")
	}
	publicKey := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
	util.Config.Runner.IdentityPublicKey = base64.RawStdEncoding.EncodeToString(publicKey)
	return nil
}
