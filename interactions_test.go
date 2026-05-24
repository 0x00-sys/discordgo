package discordgo

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVerifyInteraction(t *testing.T) {
	pubkey, privkey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Errorf("error generating signing keypair: %s", err)
	}
	timestamp := "1608597133"

	t.Run("success", func(t *testing.T) {
		body := "body"
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader(body))
		request.Header.Set("X-Signature-Timestamp", timestamp)

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		if !VerifyInteraction(request, pubkey) {
			t.Error("expected true, got false")
		}
		restored, err := ioutil.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("ReadAll restored body returned error: %v", err)
		}
		if string(restored) != body {
			t.Fatalf("restored body = %q, want %q", restored, body)
		}
	})

	t.Run("failure/modified body", func(t *testing.T) {
		body := "body"
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader("WRONG"))
		request.Header.Set("X-Signature-Timestamp", timestamp)

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		if VerifyInteraction(request, pubkey) {
			t.Error("expected false, got true")
		}
	})

	t.Run("failure/modified timestamp", func(t *testing.T) {
		body := "body"
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader("WRONG"))
		request.Header.Set("X-Signature-Timestamp", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		if VerifyInteraction(request, pubkey) {
			t.Error("expected false, got true")
		}
	})

	t.Run("failure/body too large", func(t *testing.T) {
		body := strings.Repeat("x", maxInteractionVerificationBodySize+1)
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader(body))
		request.Header.Set("X-Signature-Timestamp", timestamp)

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		if VerifyInteraction(request, pubkey) {
			t.Error("expected false, got true")
		}
	})

	t.Run("failure/bad public key length", func(t *testing.T) {
		body := "body"
		request := httptest.NewRequest("POST", "http://localhost/interaction", strings.NewReader(body))
		request.Header.Set("X-Signature-Timestamp", timestamp)

		var msg bytes.Buffer
		msg.WriteString(timestamp)
		msg.WriteString(body)
		signature := ed25519.Sign(privkey, msg.Bytes())
		request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature[:ed25519.SignatureSize]))

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("VerifyInteraction panicked with invalid public key: %v", r)
			}
		}()
		if VerifyInteraction(request, ed25519.PublicKey("bad")) {
			t.Error("expected false, got true")
		}
	})
}

func TestApplicationCommandResolvedMembersLinkUsers(t *testing.T) {
	data := []byte(`{
		"users": {
			"100": {
				"id": "100",
				"username": "user",
				"discriminator": "0001"
			}
		},
		"members": {
			"100": {
				"roles": ["200"]
			}
		}
	}`)

	var resolved ApplicationCommandInteractionDataResolved
	if err := json.Unmarshal(data, &resolved); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	member := resolved.Members["100"]
	if member == nil {
		t.Fatal("resolved member was not set")
	}
	if member.User != resolved.Users["100"] {
		t.Fatalf("member user was not linked to resolved user")
	}
	if mention := member.Mention(); mention != "<@!100>" {
		t.Fatalf("Mention = %q, want %q", mention, "<@!100>")
	}
}

func TestComponentResolvedMembersLinkUsers(t *testing.T) {
	data := []byte(`{
		"users": {
			"100": {
				"id": "100",
				"username": "user",
				"discriminator": "0001"
			}
		},
		"members": {
			"100": {
				"roles": ["200"]
			}
		}
	}`)

	var resolved ComponentInteractionDataResolved
	if err := json.Unmarshal(data, &resolved); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}

	member := resolved.Members["100"]
	if member == nil {
		t.Fatal("resolved member was not set")
	}
	if member.User != resolved.Users["100"] {
		t.Fatalf("member user was not linked to resolved user")
	}
	if mention := member.Mention(); mention != "<@!100>" {
		t.Fatalf("Mention = %q, want %q", mention, "<@!100>")
	}
}
