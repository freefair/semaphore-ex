package pro_interfaces

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// WebhookSignatureProtocolVersion identifies the canonical webhook signing protocol.
	WebhookSignatureProtocolVersion = "semaphore.webhook.v1"

	WebhookHeaderVersion   = "X-Semaphore-Webhook-Version"
	WebhookHeaderEventID   = "X-Semaphore-Event-ID"
	WebhookHeaderTimestamp = "X-Semaphore-Timestamp"
	WebhookHeaderKeyID     = "X-Semaphore-Key-ID"
	WebhookHeaderSignature = "X-Semaphore-Signature"

	WebhookSignatureMaxPayloadBytes = 256 * 1024
	webhookSignatureMaxMethodBytes  = 16
	WebhookSignatureMaxTargetBytes  = 8192
	WebhookSignatureMinEventIDBytes = 16
	WebhookSignatureMaxEventIDBytes = 128
	WebhookSignatureMaxKeyIDBytes   = 64
	webhookSigningSecretPrefix      = "swhsec_"
	webhookSigningKeyIDPrefix       = "swhkid_"
	webhookSignaturePrefix          = "v1="
)

type WebhookSignatureError string

func (e WebhookSignatureError) Error() string { return string(e) }

const (
	ErrWebhookSignatureMalformed       WebhookSignatureError = "webhook_signature_malformed"
	ErrWebhookSignatureVersion         WebhookSignatureError = "webhook_signature_version_invalid"
	ErrWebhookSignatureTimestampStale  WebhookSignatureError = "webhook_signature_timestamp_stale"
	ErrWebhookSignatureTimestampFuture WebhookSignatureError = "webhook_signature_timestamp_future"
	ErrWebhookSignatureKeyUnknown      WebhookSignatureError = "webhook_signature_key_unknown"
	ErrWebhookSignatureInvalid         WebhookSignatureError = "webhook_signature_invalid"
)

// WebhookSignatureHeaders is the transport-neutral set of protocol headers.
// Values are deliberately kept separate from http.Header so services do not
// need to depend on the HTTP transport.
type WebhookSignatureHeaders struct {
	Version   string `json:"version"`
	EventID   string `json:"event_id"`
	Timestamp string `json:"timestamp"`
	KeyID     string `json:"key_id"`
	Signature string `json:"-"`
}

// WebhookSignedRequest binds exactly the bytes that are signed. RequestTarget
// is the raw path plus raw query; callers must not parse and reconstruct it.
type WebhookSignedRequest struct {
	Version       string `json:"version"`
	Method        string `json:"method"`
	RequestTarget string `json:"request_target"`
	EventID       string `json:"event_id"`
	Timestamp     int64  `json:"timestamp"`
	KeyID         string `json:"key_id"`
	Signature     string `json:"-"`
	Payload       []byte `json:"-"`
}

// WebhookSigningKey contains non-secret metadata plus an encoded server secret.
// It must only live in process memory while a request is signed or verified.
type WebhookSigningKey struct {
	ID     string `json:"id"`
	Secret string `json:"-"`
}

// NewWebhookSigningKey creates independently random key metadata and 32-byte
// signing material. The textual secret is encoded once for one-time display or
// encrypted persistence; it is never re-encoded by signing operations.
func NewWebhookSigningKey() (WebhookSigningKey, error) {
	secret := make([]byte, sha256.Size)
	if _, err := rand.Read(secret); err != nil {
		return WebhookSigningKey{}, err
	}
	keyIDBytes := make([]byte, 12)
	if _, err := rand.Read(keyIDBytes); err != nil {
		return WebhookSigningKey{}, err
	}
	return WebhookSigningKey{
		ID:     webhookSigningKeyIDPrefix + base64.RawURLEncoding.EncodeToString(keyIDBytes),
		Secret: webhookSigningSecretPrefix + base64.RawURLEncoding.EncodeToString(secret),
	}, nil
}

// BindWebhookSignedRequest validates a transport-neutral signing request.
// Signature is optional here so outbound clients can bind before signing. Use
// BindWebhookSignedRequestFromHTTP for inbound HTTP requests, where all five
// signing headers are required exactly once.
func BindWebhookSignedRequest(method, requestTarget string, headers WebhookSignatureHeaders, payload []byte) (WebhookSignedRequest, error) {
	if headers.Version != WebhookSignatureProtocolVersion {
		return WebhookSignedRequest{}, ErrWebhookSignatureVersion
	}
	if !validWebhookMethod(method) ||
		!validWebhookRequestTarget(requestTarget) ||
		!validWebhookEventID(headers.EventID) ||
		!validWebhookKeyID(headers.KeyID) ||
		len(payload) > WebhookSignatureMaxPayloadBytes {
		return WebhookSignedRequest{}, ErrWebhookSignatureMalformed
	}
	timestamp, ok := parseWebhookUnixSeconds(headers.Timestamp)
	if !ok {
		return WebhookSignedRequest{}, ErrWebhookSignatureMalformed
	}
	if headers.Signature != "" && !validWebhookSignature(headers.Signature) {
		return WebhookSignedRequest{}, ErrWebhookSignatureMalformed
	}
	return WebhookSignedRequest{
		Version:       headers.Version,
		Method:        method,
		RequestTarget: requestTarget,
		EventID:       headers.EventID,
		Timestamp:     timestamp,
		KeyID:         headers.KeyID,
		Signature:     headers.Signature,
		Payload:       append([]byte(nil), payload...),
	}, nil
}

// BindWebhookSignedRequestFromHTTP rejects absent or repeated protocol headers
// before preserving the request method, raw target, and body unchanged.
func BindWebhookSignedRequestFromHTTP(method, requestTarget string, headers http.Header, payload []byte) (WebhookSignedRequest, error) {
	bound := WebhookSignatureHeaders{}
	values := []struct {
		name string
		dest *string
	}{
		{WebhookHeaderVersion, &bound.Version},
		{WebhookHeaderEventID, &bound.EventID},
		{WebhookHeaderTimestamp, &bound.Timestamp},
		{WebhookHeaderKeyID, &bound.KeyID},
		{WebhookHeaderSignature, &bound.Signature},
	}
	for _, header := range values {
		value, ok := exactlyOneWebhookHeader(headers, header.name)
		if !ok {
			return WebhookSignedRequest{}, ErrWebhookSignatureMalformed
		}
		*header.dest = value
	}
	return BindWebhookSignedRequest(method, requestTarget, bound, payload)
}

// CanonicalWebhookSignatureBytes returns the length-framed protocol message.
func CanonicalWebhookSignatureBytes(request WebhookSignedRequest) ([]byte, error) {
	if request.Version != WebhookSignatureProtocolVersion {
		return nil, ErrWebhookSignatureVersion
	}
	if !validWebhookMethod(request.Method) ||
		!validWebhookRequestTarget(request.RequestTarget) ||
		!validWebhookEventID(request.EventID) ||
		!validWebhookKeyID(request.KeyID) ||
		len(request.Payload) > WebhookSignatureMaxPayloadBytes {
		return nil, ErrWebhookSignatureMalformed
	}
	// The request stores an integer timestamp. BindWebhookSignedRequest is the
	// only public conversion point from text, so the signed form is canonical.
	length := 2 + len(request.Version) + 2 + len(request.Method) + 4 + len(request.RequestTarget) +
		2 + len(request.EventID) + 8 + 2 + len(request.KeyID) + 8 + len(request.Payload)
	canonical := make([]byte, 0, length)
	canonical = appendWebhookU16(canonical, request.Version)
	canonical = appendWebhookU16(canonical, request.Method)
	canonical = appendWebhookU32(canonical, request.RequestTarget)
	canonical = appendWebhookU16(canonical, request.EventID)
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], uint64(request.Timestamp))
	canonical = append(canonical, timestamp[:]...)
	canonical = appendWebhookU16(canonical, request.KeyID)
	var payloadLength [8]byte
	binary.BigEndian.PutUint64(payloadLength[:], uint64(len(request.Payload)))
	canonical = append(canonical, payloadLength[:]...)
	canonical = append(canonical, request.Payload...)
	return canonical, nil
}

func SignWebhookRequest(secret string, request WebhookSignedRequest) (string, error) {
	key, err := decodeWebhookSigningSecret(secret)
	if err != nil {
		return "", ErrWebhookSignatureMalformed
	}
	canonical, err := CanonicalWebhookSignatureBytes(request)
	if err != nil {
		return "", err
	}
	return webhookSignaturePrefix + hex.EncodeToString(webhookSignatureMAC(key, canonical)), nil
}

// VerifyWebhookSignature checks all supplied current/next candidates before
// deciding. This avoids revealing which configured key matched by early exit.
func VerifyWebhookSignature(request WebhookSignedRequest, signature string, candidates []WebhookSigningKey, now time.Time) error {
	return verifyWebhookSignature(request, signature, candidates, now, webhookSignatureMAC)
}

func verifyWebhookSignature(request WebhookSignedRequest, signature string, candidates []WebhookSigningKey, now time.Time, calculateMAC func([]byte, []byte) []byte) error {
	if !validWebhookSignature(signature) {
		return ErrWebhookSignatureMalformed
	}
	if _, err := CanonicalWebhookSignatureBytes(request); err != nil {
		return err
	}
	if request.Timestamp < now.Add(-5*time.Minute).Unix() {
		return ErrWebhookSignatureTimestampStale
	}
	if request.Timestamp > now.Add(5*time.Minute).Unix() {
		return ErrWebhookSignatureTimestampFuture
	}
	provided, _ := hex.DecodeString(strings.TrimPrefix(signature, webhookSignaturePrefix))
	canonical, _ := CanonicalWebhookSignatureBytes(request)
	matchedKeyID := false
	matchedSignature := false
	for _, candidate := range candidates {
		if !validWebhookKeyID(candidate.ID) {
			continue
		}
		key, err := decodeWebhookSigningSecret(candidate.Secret)
		if err != nil {
			continue
		}
		candidateMatches := hmac.Equal(calculateMAC(key, canonical), provided)
		keyIDMatches := candidate.ID == request.KeyID
		matchedKeyID = matchedKeyID || keyIDMatches
		matchedSignature = matchedSignature || (keyIDMatches && candidateMatches)
	}
	if !matchedKeyID {
		return ErrWebhookSignatureKeyUnknown
	}
	if !matchedSignature {
		return ErrWebhookSignatureInvalid
	}
	return nil
}

func webhookSignatureMAC(key, canonical []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	return mac.Sum(nil)
}

// VerifyBoundWebhookSignature verifies the header signature carried by a bound
// inbound request.
func VerifyBoundWebhookSignature(request WebhookSignedRequest, candidates []WebhookSigningKey, now time.Time) error {
	return VerifyWebhookSignature(request, request.Signature, candidates, now)
}

// WebhookEventIdentityHash is a stable replay identity. It intentionally does
// not contain a key generation, key ID, secret, timestamp, or request body.
func WebhookEventIdentityHash(triggerID int, eventID string) ([sha256.Size]byte, error) {
	if triggerID <= 0 || !validWebhookEventID(eventID) {
		return [sha256.Size]byte{}, ErrWebhookSignatureMalformed
	}
	input := make([]byte, 0, 32+len(eventID))
	input = appendWebhookU16(input, "semaphore.webhook.event.v1")
	var trigger [8]byte
	binary.BigEndian.PutUint64(trigger[:], uint64(triggerID))
	input = append(input, trigger[:]...)
	input = appendWebhookU16(input, eventID)
	return sha256.Sum256(input), nil
}

func WebhookEventIdentityHashHex(triggerID int, eventID string) (string, error) {
	hash, err := WebhookEventIdentityHash(triggerID, eventID)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash[:]), nil
}

// ValidWebhookSigningKeyID permits persistence and transport layers to reject
// malformed non-secret key metadata without accessing signing material.
func ValidWebhookSigningKeyID(keyID string) bool {
	return validWebhookKeyID(keyID)
}

func exactlyOneWebhookHeader(headers http.Header, name string) (string, bool) {
	var values []string
	for candidateName, candidateValues := range headers {
		if strings.EqualFold(candidateName, name) {
			values = append(values, candidateValues...)
		}
	}
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	return values[0], true
}

func validWebhookMethod(value string) bool {
	if len(value) == 0 || len(value) > webhookSignatureMaxMethodBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 'A' || value[i] > 'Z' {
			return false
		}
	}
	return true
}

func validWebhookRequestTarget(value string) bool {
	if len(value) == 0 || len(value) > WebhookSignatureMaxTargetBytes || value[0] != '/' {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] == '#' || value[i] == 0x7f {
			return false
		}
	}
	return true
}

func validWebhookEventID(value string) bool {
	if len(value) < WebhookSignatureMinEventIDBytes || len(value) > WebhookSignatureMaxEventIDBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '.' && c != '_' && c != '-' && c != ':' {
			return false
		}
	}
	return true
}

func validWebhookKeyID(value string) bool {
	if len(value) < len(webhookSigningKeyIDPrefix)+1 || len(value) > WebhookSignatureMaxKeyIDBytes || !strings.HasPrefix(value, webhookSigningKeyIDPrefix) {
		return false
	}
	for i := len(webhookSigningKeyIDPrefix); i < len(value); i++ {
		c := value[i]
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func parseWebhookUnixSeconds(value string) (int64, bool) {
	if value == "0" {
		return 0, true
	}
	start := 0
	if strings.HasPrefix(value, "-") {
		start = 1
	}
	if start == len(value) || value[start] == '0' || len(value) > 20 {
		return 0, false
	}
	for i := start; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil
}

func validWebhookSignature(value string) bool {
	if !strings.HasPrefix(value, webhookSignaturePrefix) || len(value) != len(webhookSignaturePrefix)+sha256.Size*2 {
		return false
	}
	for i := len(webhookSignaturePrefix); i < len(value); i++ {
		if !(value[i] >= '0' && value[i] <= '9') && !(value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

func decodeWebhookSigningSecret(value string) ([]byte, error) {
	if !strings.HasPrefix(value, webhookSigningSecretPrefix) {
		return nil, ErrWebhookSignatureMalformed
	}
	encoded := strings.TrimPrefix(value, webhookSigningSecretPrefix)
	secret, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(secret) != sha256.Size || base64.RawURLEncoding.EncodeToString(secret) != encoded {
		return nil, ErrWebhookSignatureMalformed
	}
	return secret, nil
}

func appendWebhookU16(buffer []byte, value string) []byte {
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(value)))
	buffer = append(buffer, length[:]...)
	return append(buffer, value...)
}

func appendWebhookU32(buffer []byte, value string) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	buffer = append(buffer, length[:]...)
	return append(buffer, value...)
}
