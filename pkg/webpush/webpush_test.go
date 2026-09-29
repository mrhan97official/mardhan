package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

// RFC 8291 Appendix A: the receiver's private key, auth secret and the
// encrypted message a conforming sender produced for them.
const (
	rfcReceiverPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcReceiverPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcAuthSecret      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcBody            = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	rfcPlaintext       = "When I grow up, I want to be a watermelon"
)

func mustB64(t *testing.T, value string) []byte {
	t.Helper()
	data, err := decodeB64(value)
	if err != nil { t.Fatalf("base64 %q: %v", value, err) }
	return data
}

func testHMAC(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

// decryptForTest is the browser side of RFC 8291, written independently of
// encrypt(). It is itself proven against the RFC's own message below, so a
// round trip through it proves encrypt() produces what browsers accept.
func decryptForTest(t *testing.T, receiver *ecdh.PrivateKey, authSecret, body []byte) []byte {
	t.Helper()
	if len(body) < 21 { t.Fatalf("body too short: %d bytes", len(body)) }
	salt := body[:16]
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != 4096 { t.Fatalf("record size = %d, want 4096", rs) }
	idLength := int(body[20])
	if idLength != 65 || len(body) < 21+idLength { t.Fatalf("key id length = %d, want 65", idLength) }
	senderPublicBytes := body[21 : 21+idLength]
	ciphertext := body[21+idLength:]
	senderPublic, err := ecdh.P256().NewPublicKey(senderPublicBytes)
	if err != nil { t.Fatalf("sender public key: %v", err) }
	shared, err := receiver.ECDH(senderPublic)
	if err != nil { t.Fatalf("ECDH: %v", err) }
	receiverPublic := receiver.PublicKey().Bytes()
	keyInfo := []byte("WebPush: info\x00")
	keyInfo = append(keyInfo, receiverPublic...)
	keyInfo = append(keyInfo, senderPublicBytes...)
	keyInfo = append(keyInfo, 1)
	ikm := testHMAC(testHMAC(authSecret, shared), keyInfo)
	prk := testHMAC(salt, ikm)
	cek := testHMAC(prk, []byte("Content-Encoding: aes128gcm\x00\x01"))[:16]
	nonce := testHMAC(prk, []byte("Content-Encoding: nonce\x00\x01"))[:12]
	block, err := aes.NewCipher(cek)
	if err != nil { t.Fatal(err) }
	gcm, err := cipher.NewGCM(block)
	if err != nil { t.Fatal(err) }
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil { t.Fatalf("GCM open (wrong keys or derivation): %v", err) }
	// Strip padding zeros, then the 0x02 last-record delimiter.
	plaintext = []byte(strings.TrimRight(string(plaintext), "\x00"))
	if len(plaintext) == 0 || plaintext[len(plaintext)-1] != 2 { t.Fatalf("missing last-record delimiter 0x02") }
	return plaintext[:len(plaintext)-1]
}

func rfcReceiver(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	receiver, err := ecdh.P256().NewPrivateKey(mustB64(t, rfcReceiverPrivate))
	if err != nil { t.Fatal(err) }
	if got := b64.EncodeToString(receiver.PublicKey().Bytes()); got != rfcReceiverPublic {
		t.Fatalf("RFC receiver public key = %s, want %s", got, rfcReceiverPublic)
	}
	return receiver
}

func TestDecryptHelperMatchesRFC8291(t *testing.T) {
	got := decryptForTest(t, rfcReceiver(t), mustB64(t, rfcAuthSecret), mustB64(t, rfcBody))
	if string(got) != rfcPlaintext { t.Fatalf("decrypted %q, want %q", got, rfcPlaintext) }
}

func TestEncryptRoundTripsLikeABrowser(t *testing.T) {
	receiver := rfcReceiver(t)
	authSecret := mustB64(t, rfcAuthSecret)
	for _, message := range []string{rfcPlaintext, `{"title":"✅ Deploy berhasil","body":"kasir-f-b sudah online.","url":"/deployments"}`, ""} {
		body, err := encrypt(receiver.PublicKey().Bytes(), authSecret, []byte(message))
		if err != nil { t.Fatalf("encrypt: %v", err) }
		if got := decryptForTest(t, receiver, authSecret, body); string(got) != message {
			t.Fatalf("round trip = %q, want %q", got, message)
		}
	}
}

func TestEncryptUsesFreshKeysEachTime(t *testing.T) {
	receiver := rfcReceiver(t)
	first, err := encrypt(receiver.PublicKey().Bytes(), mustB64(t, rfcAuthSecret), []byte("sama"))
	if err != nil { t.Fatal(err) }
	second, err := encrypt(receiver.PublicKey().Bytes(), mustB64(t, rfcAuthSecret), []byte("sama"))
	if err != nil { t.Fatal(err) }
	if string(first) == string(second) { t.Fatal("two encryptions of the same message are identical; salt/sender key must be random") }
}

func TestEncryptRejectsInvalidDeviceKey(t *testing.T) {
	if _, err := encrypt([]byte{4, 1, 2, 3}, mustB64(t, rfcAuthSecret), []byte("x")); err == nil {
		t.Fatal("encrypt accepted an invalid device public key")
	}
}

func newTestKeys(t *testing.T) (*vapid, string, string) {
	t.Helper()
	generated, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil { t.Fatalf("generate key: %v", err) }
	public, private := b64.EncodeToString(generated.PublicKey().Bytes()), b64.EncodeToString(generated.Bytes())
	keys, err := parseKeys(public, private)
	if err != nil { t.Fatalf("parseKeys: %v", err) }
	return keys, public, private
}

func TestVAPIDTokenIsValidES256(t *testing.T) {
	keys, public, _ := newTestKeys(t)
	keys.subject = "https://devcontrol.example.com"
	if keys.publicText != public { t.Fatalf("publicText = %s, want %s", keys.publicText, public) }
	token, err := keys.token("https://fcm.googleapis.com")
	if err != nil { t.Fatal(err) }
	parts := strings.Split(token, ".")
	if len(parts) != 3 { t.Fatalf("JWT has %d parts, want 3", len(parts)) }

	var header map[string]string
	if err := json.Unmarshal(mustB64(t, parts[0]), &header); err != nil || header["alg"] != "ES256" || header["typ"] != "JWT" {
		t.Fatalf("header = %v (%v)", header, err)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(mustB64(t, parts[1]), &claims); err != nil { t.Fatal(err) }
	if claims.Aud != "https://fcm.googleapis.com" || claims.Sub != "https://devcontrol.example.com" {
		t.Fatalf("claims = %+v", claims)
	}
	// RFC 8292: exp must be in the future and at most 24 hours away.
	if until := time.Until(time.Unix(claims.Exp, 0)); until <= 0 || until > 24*time.Hour {
		t.Fatalf("exp is %v from now; must be within (0, 24h]", until)
	}

	signature := mustB64(t, parts[2])
	if len(signature) != 64 { t.Fatalf("signature is %d bytes, want raw r||s of 64", len(signature)) }
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(&keys.private.PublicKey, digest[:], r, s) { t.Fatal("ES256 signature does not verify with the VAPID public key") }
}

func TestParseKeysRejectsMismatchedPair(t *testing.T) {
	_, public, _ := newTestKeys(t)
	_, _, otherPrivate := newTestKeys(t)
	if _, err := parseKeys(public, otherPrivate); err == nil { t.Fatal("parseKeys accepted a public key from a different pair") }
	if _, err := parseKeys("bukan-kunci", otherPrivate); err == nil { t.Fatal("parseKeys accepted an invalid public key") }
}

func TestDecodeB64AcceptsStandardAndURLForms(t *testing.T) {
	want := []byte{0xfb, 0xff, 0xfe}
	for _, input := range []string{"-__-", "+//+", " -__- "} {
		got, err := decodeB64(input)
		if err != nil || string(got) != string(want) { t.Fatalf("decodeB64(%q) = %x, %v", input, got, err) }
	}
}

func TestValidEndpointOnlyAllowsBrowserPushServices(t *testing.T) {
	allowed := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://web.push.apple.com/abc",
		"https://wns2-par02p.notify.windows.com/w/?token=abc",
	}
	for _, endpoint := range allowed {
		if !validEndpoint(endpoint) { t.Errorf("validEndpoint(%q) = false, want true", endpoint) }
	}
	denied := []string{
		"http://fcm.googleapis.com/fcm/send/abc",       // not https
		"https://evil.example.com/fcm.googleapis.com",  // wrong host
		"https://googleapis.com.evil.example.com/x",    // suffix trick
		"https://user:pass@fcm.googleapis.com/x",       // credentials
		"https://fcm.googleapis.com:8443/x",            // custom port
		"https://" + strings.Repeat("a", 1100) + ".googleapis.com/x",
		"",
	}
	for _, endpoint := range denied {
		if validEndpoint(endpoint) { t.Errorf("validEndpoint(%q) = true, want false", endpoint) }
	}
}

func TestJoinEventsKeepsOrderAndAdminOnlyEvents(t *testing.T) {
	requested := []string{EventApp404, EventConfirm, "bukan-event", EventDeploy, EventDeploy}
	if got := joinEvents(requested, true); got != "deploy,confirm,app404" {
		t.Fatalf("admin events = %q, want deploy,confirm,app404", got)
	}
	if got := joinEvents(requested, false); got != "deploy,app404" {
		t.Fatalf("member events = %q, want deploy,app404 (confirm is admin-only)", got)
	}
	if got := joinEvents(nil, true); got != "" { t.Fatalf("no events = %q, want empty", got) }
}

func TestSplitEventsIgnoresUnknown(t *testing.T) {
	events := splitEvents("deploy,,app404,rahasia")
	if len(events) != 2 || !events[EventDeploy] || !events[EventApp404] { t.Fatalf("splitEvents = %v", events) }
}

func TestTrimCountsCharactersNotBytes(t *testing.T) {
	if got := trim("  ✅✅✅✅  ", 3); got != "✅✅…" { t.Fatalf("trim = %q, want ✅✅…", got) }
	if got := trim("pendek", 10); got != "pendek" { t.Fatalf("trim = %q", got) }
}
