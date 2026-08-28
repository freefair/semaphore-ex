package util

// AccessKeyEncryptionEnabled reports whether access-key and task-secret values
// are protected by an active encryption key rather than legacy passthrough.
func (conf *ConfigType) AccessKeyEncryptionEnabled() bool {
	return conf.currentKeyset().accessID != ""
}

// OptionEncryptionEnabled reports whether option values are protected by an
// active option key or its access-key fallback. Without either key,
// EncryptOption only base64-encodes values for legacy compatibility.
func (conf *ConfigType) OptionEncryptionEnabled() bool {
	ks := conf.currentKeyset()
	return ks.optionID != "" || ks.accessID != ""
}
