package pro_interfaces

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

const webhookTestSecret = "swhsec_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func webhookTestRequest(t *testing.T) WebhookSignedRequest {
	t.Helper()
	request, err := BindWebhookSignedRequest("POST", "/hooks/run?x=%2F", WebhookSignatureHeaders{
		Version: WebhookSignatureProtocolVersion, EventID: "evt_0123456789abcd", Timestamp: "1700000000", KeyID: "swhkid_test_0001",
	}, []byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestWebhookCanonicalSignatureVector(t *testing.T) {
	request := webhookTestRequest(t)
	canonical, err := CanonicalWebhookSignatureBytes(request)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := []byte{
		0, 20, 's', 'e', 'm', 'a', 'p', 'h', 'o', 'r', 'e', '.', 'w', 'e', 'b', 'h', 'o', 'o', 'k', '.', 'v', '1',
		0, 4, 'P', 'O', 'S', 'T', 0, 0, 0, 16, '/', 'h', 'o', 'o', 'k', 's', '/', 'r', 'u', 'n', '?', 'x', '=', '%', '2', 'F',
		0, 18, 'e', 'v', 't', '_', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 0, 0, 0, 0, 101, 83, 241, 0,
		0, 16, 's', 'w', 'h', 'k', 'i', 'd', '_', 't', 'e', 's', 't', '_', '0', '0', '0', '1',
		0, 0, 0, 0, 0, 0, 0, 11, '{', '"', 'o', 'k', '"', ':', 't', 'r', 'u', 'e', '}',
	}
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatalf("canonical bytes mismatch: %x", canonical)
	}
	signature, err := SignWebhookRequest(webhookTestSecret, request)
	if err != nil {
		t.Fatal(err)
	}
	const wantSignature = "v1=0f826cf54ef1745b79f87360111254f17b4589ccb27f2ad88b1d7a1f88ed52f2"
	if signature != wantSignature {
		t.Fatalf("signature = %s", signature)
	}
}

func TestWebhookSignatureChangesForEveryBoundField(t *testing.T) {
	request := webhookTestRequest(t)
	baseline, err := SignWebhookRequest(webhookTestSecret, request)
	if err != nil {
		t.Fatal(err)
	}
	variants := []WebhookSignedRequest{
		func() WebhookSignedRequest { v := request; v.Method = "PUT"; return v }(),
		func() WebhookSignedRequest { v := request; v.RequestTarget = "/hooks/other?x=%2F"; return v }(),
		func() WebhookSignedRequest { v := request; v.EventID = "evt_1123456789abcd"; return v }(),
		func() WebhookSignedRequest { v := request; v.Timestamp++; return v }(),
		func() WebhookSignedRequest { v := request; v.KeyID = "swhkid_test_0002"; return v }(),
		func() WebhookSignedRequest {
			v := request
			v.Payload = append([]byte(nil), v.Payload...)
			v.Payload[2] = 'X'
			return v
		}(),
	}
	for _, variant := range variants {
		signature, err := SignWebhookRequest(webhookTestSecret, variant)
		if err != nil || signature == baseline {
			t.Fatalf("field mutation did not alter MAC: signature=%q err=%v", signature, err)
		}
	}
}

func TestVerifyWebhookSignatureTimestampBoundariesAndRotationOverlap(t *testing.T) {
	now := time.Unix(1700000000, 0)
	request := webhookTestRequest(t)
	request.Timestamp = now.Add(-5 * time.Minute).Unix()
	signature, err := SignWebhookRequest(webhookTestSecret, request)
	if err != nil {
		t.Fatal(err)
	}
	keys := []WebhookSigningKey{
		{ID: "swhkid_old_0001", Secret: "swhsec_AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"},
		{ID: request.KeyID, Secret: webhookTestSecret},
	}
	if err := VerifyWebhookSignature(request, signature, keys, now); err != nil {
		t.Fatalf("exact stale boundary rejected: %v", err)
	}
	request.Timestamp = now.Add(5 * time.Minute).Unix()
	signature, _ = SignWebhookRequest(webhookTestSecret, request)
	if err := VerifyWebhookSignature(request, signature, keys, now); err != nil {
		t.Fatalf("exact future boundary rejected: %v", err)
	}
	request.Timestamp = now.Add(-5*time.Minute - time.Second).Unix()
	signature, _ = SignWebhookRequest(webhookTestSecret, request)
	if err := VerifyWebhookSignature(request, signature, keys, now); err != ErrWebhookSignatureTimestampStale {
		t.Fatalf("stale error = %v", err)
	}
	request.Timestamp = now.Add(5*time.Minute + time.Second).Unix()
	signature, _ = SignWebhookRequest(webhookTestSecret, request)
	if err := VerifyWebhookSignature(request, signature, keys, now); err != ErrWebhookSignatureTimestampFuture {
		t.Fatalf("future error = %v", err)
	}
}

func TestVerifyWebhookSignatureEvaluatesCurrentAndNextCandidates(t *testing.T) {
	request := webhookTestRequest(t)
	now := time.Unix(request.Timestamp, 0)
	oldSecret := "swhsec_AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
	request.KeyID = "swhkid_current_0001"
	signature, err := SignWebhookRequest(webhookTestSecret, request)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = verifyWebhookSignature(request, signature, []WebhookSigningKey{
		{ID: request.KeyID, Secret: webhookTestSecret},
		{ID: "swhkid_next_0001", Secret: oldSecret},
	}, now, func(key, canonical []byte) []byte {
		calls++
		return webhookSignatureMAC(key, canonical)
	})
	if err != nil || calls != 2 {
		t.Fatalf("verify error=%v mac calls=%d, want two", err, calls)
	}
	request.KeyID = "swhkid_unknown_0001"
	if err := VerifyWebhookSignature(request, signature, []WebhookSigningKey{{ID: "swhkid_current_0001", Secret: webhookTestSecret}}, now); err != ErrWebhookSignatureKeyUnknown {
		t.Fatalf("unknown key error = %v", err)
	}
	request.KeyID = "swhkid_current_0001"
	if err := VerifyWebhookSignature(request, "v1="+strings.Repeat("0", 64), []WebhookSigningKey{{ID: request.KeyID, Secret: webhookTestSecret}}, now); err != ErrWebhookSignatureInvalid {
		t.Fatalf("invalid MAC error = %v", err)
	}
}

func TestWebhookRawRequestTargetIsNotNormalized(t *testing.T) {
	request := webhookTestRequest(t)
	baseline, err := SignWebhookRequest(webhookTestSecret, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/hooks/run?x=/",
		"/hooks/run?x=%2f",
		"/hooks/run?x=%2F&x=1",
		"/hooks/run?x=1&x=%2F",
		"/hooks/run?x=+",
		"/hooks/run?x=%20",
	} {
		variant := request
		variant.RequestTarget = target
		signature, err := SignWebhookRequest(webhookTestSecret, variant)
		if err != nil || signature == baseline {
			t.Fatalf("target %q was normalized or rejected: signature=%q err=%v", target, signature, err)
		}
	}
}

func TestBindWebhookSignedRequestRejectsMalformedAndBoundedInput(t *testing.T) {
	headers := WebhookSignatureHeaders{Version: WebhookSignatureProtocolVersion, EventID: "evt_0123456789abcd", Timestamp: "1700000000", KeyID: "swhkid_test_0001"}
	invalids := []WebhookSignatureHeaders{
		func() WebhookSignatureHeaders { v := headers; v.Timestamp = "01700000000"; return v }(),
		func() WebhookSignatureHeaders { v := headers; v.Timestamp = "+1700000000"; return v }(),
		func() WebhookSignatureHeaders { v := headers; v.Timestamp = "1700000000.0"; return v }(),
		func() WebhookSignatureHeaders { v := headers; v.EventID = "short"; return v }(),
		func() WebhookSignatureHeaders { v := headers; v.KeyID = "secret"; return v }(),
		func() WebhookSignatureHeaders {
			v := headers
			v.KeyID = "swhkid_" + strings.Repeat("a", WebhookSignatureMaxKeyIDBytes)
			return v
		}(),
		func() WebhookSignatureHeaders { v := headers; v.Signature = "v1=" + strings.Repeat("A", 64); return v }(),
		func() WebhookSignatureHeaders { v := headers; v.Signature = "v1=" + strings.Repeat("0", 63); return v }(),
	}
	for _, invalid := range invalids {
		if _, err := BindWebhookSignedRequest("POST", "/hook", invalid, nil); err != ErrWebhookSignatureMalformed {
			t.Fatalf("error = %v for %#v", err, invalid)
		}
	}
	unsupportedVersion := headers
	unsupportedVersion.Version = "semaphore.webhook.v2"
	if _, err := BindWebhookSignedRequest("POST", "/hook", unsupportedVersion, nil); err != ErrWebhookSignatureVersion {
		t.Fatalf("version error = %v", err)
	}
	if _, err := BindWebhookSignedRequest("post", "/hook", headers, nil); err != ErrWebhookSignatureMalformed {
		t.Fatalf("lowercase method error = %v", err)
	}
	if _, err := BindWebhookSignedRequest("POST", "/hook", headers, make([]byte, WebhookSignatureMaxPayloadBytes+1)); err != ErrWebhookSignatureMalformed {
		t.Fatalf("payload bound error = %v", err)
	}
	if _, err := BindWebhookSignedRequest("POST", "/"+strings.Repeat("a", WebhookSignatureMaxTargetBytes), headers, nil); err != ErrWebhookSignatureMalformed {
		t.Fatalf("target bound error = %v", err)
	}
	validSignature, _ := SignWebhookRequest(webhookTestSecret, webhookTestRequest(t))
	headers.Signature = validSignature
	httpHeaders := http.Header{
		WebhookHeaderVersion: {headers.Version, headers.Version}, WebhookHeaderEventID: {headers.EventID}, WebhookHeaderTimestamp: {headers.Timestamp}, WebhookHeaderKeyID: {headers.KeyID}, WebhookHeaderSignature: {headers.Signature},
	}
	if _, err := BindWebhookSignedRequestFromHTTP("POST", "/hook", httpHeaders, nil); err != ErrWebhookSignatureMalformed {
		t.Fatalf("duplicate header error = %v", err)
	}
}

func TestWebhookEventIdentityIsRotationIndependent(t *testing.T) {
	first, err := WebhookEventIdentityHash(7, "evt_0123456789abcd")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := WebhookEventIdentityHash(7, "evt_0123456789abcd")
	otherEvent, _ := WebhookEventIdentityHash(7, "evt_1123456789abcd")
	otherTrigger, _ := WebhookEventIdentityHash(8, "evt_0123456789abcd")
	if first != second || first == otherEvent || first == otherTrigger {
		t.Fatal("event identity is not stable and trigger/event scoped")
	}
}

func TestNewWebhookSigningKeyUsesBoundedNonSecretIDAnd32ByteSecret(t *testing.T) {
	key, err := NewWebhookSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidWebhookSigningKeyID(key.ID) {
		t.Fatalf("generated key ID is invalid: %q", key.ID)
	}
	request := webhookTestRequest(t)
	request.KeyID = key.ID
	if _, err := SignWebhookRequest(key.Secret, request); err != nil {
		t.Fatalf("generated secret cannot sign: %v", err)
	}
	serialized, err := json.Marshal(key)
	if err != nil || bytes.Contains(serialized, []byte(key.Secret)) {
		t.Fatalf("secret escaped JSON redaction: %q, err=%v", serialized, err)
	}
}
