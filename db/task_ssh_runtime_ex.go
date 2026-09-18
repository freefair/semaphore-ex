package db

// ResolvedTaskSSHKey is the in-memory task credential descriptor. Binding is
// persisted only in the private execution snapshot; Key is populated at
// dispatch through the existing key-store authority and never serialized.
type ResolvedTaskSSHKey struct {
	Binding SSHKeyBinding `json:"binding"`
	Key     AccessKey     `json:"-"`
}
