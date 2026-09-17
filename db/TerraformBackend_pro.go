package db

// TerraformStateLock is the opaque HTTP-backend lock payload persisted for one
// inventory. Info is returned only to another authenticated backend client.
type TerraformStateLock struct {
	ID   string `db:"id"`
	Info string `db:"info"`
}

// TerraformStateCipherStore participates in key rotation and retirement
// checks; callers receive only ciphertext envelopes, never decrypted state.
type TerraformStateCipherStore interface {
	RekeyTerraformStates(oldKey string) error
	ListTerraformStateCiphertexts() ([]string, error)
}
