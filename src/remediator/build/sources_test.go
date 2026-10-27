package build

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	c "github.com/asafg8/Phosphor/src/contract"
)

// GPG verification runs in-agent; hosts without gpg (minimal AL2023) can still build P6 8.x.
func TestBUG050_InAgentSignature(t *testing.T) {
	d := t.TempDir()
	ent, err := openpgp.NewEntity("rm", "", "rm@php.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	var kr bytes.Buffer
	w, _ := armor.Encode(&kr, openpgp.PublicKeyType, nil)
	_ = ent.Serialize(w)
	w.Close()
	keyring, data, sig := filepath.Join(d, "k.asc"), filepath.Join(d, "php.tar.xz"), filepath.Join(d, "php.tar.xz.asc")
	_ = os.WriteFile(keyring, kr.Bytes(), 0o644)
	_ = os.WriteFile(data, []byte("php source"), 0o644)
	var sb bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&sb, ent, strings.NewReader("php source"), nil); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(sig, sb.Bytes(), 0o644)
	fpr := strings.ToUpper(hex.EncodeToString(ent.PrimaryKey.Fingerprint))

	if got, reason := checkDetachedSignature(keyring, data, sig, []string{fpr}); reason != "" || got != fpr {
		t.Fatalf("good signature: %q %q", got, reason)
	}
	if _, reason := checkDetachedSignature(keyring, data, sig, []string{"E60913E4DF209907D8E30D96659A97C9CF2A795A"}); reason != "signer-not-pinned" {
		t.Fatalf("unpinned signer: %q", reason)
	}
	_ = os.WriteFile(data, []byte("php source tampered"), 0o644)
	if _, reason := checkDetachedSignature(keyring, data, sig, []string{fpr}); reason != "bad-signature" {
		t.Fatalf("tampered: %q", reason)
	}
}

// Real php.net release (opt-in: PHP82_TARBALL=/path/php-8.2.35.tar.xz with its .asc next to it).
func TestBUG050_RealPHP82(t *testing.T) {
	tb := os.Getenv("PHP82_TARBALL")
	if tb == "" {
		t.Skip("set PHP82_TARBALL")
	}
	fpr, reason := checkDetachedSignature("../../../data/keys/php-keyring.asc", tb, tb+".asc",
		[]string{"E60913E4DF209907D8E30D96659A97C9CF2A795A", "39B641343D8C104B2B146DC3F9C39DC0B9698544"})
	if reason != "" || fpr != "E60913E4DF209907D8E30D96659A97C9CF2A795A" {
		t.Fatalf("php-8.2.35: %q %q", fpr, reason)
	}
}

func TestNormalizeFailureDoc(t *testing.T) {
	// build.json goes out through rt.WriteDoc → c.Marshal, which turns nil arrays into []
	b, _ := c.Marshal(&c.Build{})
	if bytes.Contains(b, []byte("null")) && (bytes.Contains(b, []byte(`"sources":null`)) || bytes.Contains(b, []byte(`"exts":null`))) {
		t.Fatalf("null arrays: %s", b)
	}
}

// lockFprs reads a data/php-<minor>.lock's pinned release-manager fingerprints.
func lockFprs(t *testing.T, minor string) []string {
	t.Helper()
	b, err := os.ReadFile("../../../data/php-" + minor + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	var l struct {
		Signature struct {
			Fprs []string `json:"release_manager_fprs"`
		} `json:"signature"`
	}
	if json.Unmarshal(b, &l) != nil || len(l.Signature.Fprs) == 0 {
		t.Fatalf("php-%s.lock: no release_manager_fprs", minor)
	}
	return l.Signature.Fprs
}

// 8.4.27 is the first EdDSA-signed tarball (Calvin Buckley, legacy EdDSA/ed25519; 8.5.12 is RSA): the real .asc
// parses, its issuer is in the vendored keyring with a key go-crypto can verify, and that key is a pinned RM.
func TestPHP8xSignaturesKnown(t *testing.T) {
	kf, err := os.Open("../../../data/keys/php-keyring.asc")
	if err != nil {
		t.Fatal(err)
	}
	kr, err := openpgp.ReadArmoredKeyRing(kf)
	kf.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ minor, asc string }{{"8.4", "php-8.4.27.tar.xz.asc"}, {"8.5", "php-8.5.12.tar.xz.asc"}} {
		f, _ := os.Open(filepath.Join("testdata", tc.asc))
		blk, err := armor.Decode(f)
		if err != nil {
			t.Fatalf("%s: %v", tc.asc, err)
		}
		p, err := packet.Read(blk.Body)
		f.Close()
		sig, ok := p.(*packet.Signature)
		if err != nil || !ok || sig.IssuerKeyId == nil {
			t.Fatalf("%s: not a signature packet: %v", tc.asc, err)
		}
		keys := kr.KeysById(*sig.IssuerKeyId)
		if len(keys) == 0 {
			t.Fatalf("%s: signer %X not in data/keys/php-keyring.asc", tc.asc, *sig.IssuerKeyId)
		}
		fpr := strings.ToUpper(hex.EncodeToString(keys[0].Entity.PrimaryKey.Fingerprint))
		if !fprAllowed(fpr, lockFprs(t, tc.minor)) {
			t.Errorf("%s: signer %s not pinned in php-%s.lock", tc.asc, fpr, tc.minor)
		}
		if tc.minor == "8.4" && sig.PubKeyAlgo != packet.PubKeyAlgoEdDSA {
			t.Errorf("8.4.27 expected EdDSA, got algo %d", sig.PubKeyAlgo)
		}
	}
}

// The real tarballs (opt-in: PHP8X_DIR=<dir with php-8.4.27.tar.xz and php-8.5.12.tar.xz>): the in-agent verify
// accepts both signatures (EdDSA and RSA) with the vendored keyring and the lock's pinned fingerprints.
func TestPHP8xRealSignatures(t *testing.T) {
	d := os.Getenv("PHP8X_DIR")
	if d == "" {
		t.Skip("set PHP8X_DIR")
	}
	for _, tc := range []struct{ minor, file string }{{"8.4", "php-8.4.27.tar.xz"}, {"8.5", "php-8.5.12.tar.xz"}} {
		fpr, reason := checkDetachedSignature("../../../data/keys/php-keyring.asc", filepath.Join(d, tc.file),
			filepath.Join("testdata", tc.file+".asc"), lockFprs(t, tc.minor))
		if reason != "" {
			t.Errorf("%s: %s (signer %s)", tc.file, reason, fpr)
		} else {
			t.Logf("%s: good signature, signer %s", tc.file, fpr)
		}
	}
	// and the EdDSA signature does not verify another file
	if _, reason := checkDetachedSignature("../../../data/keys/php-keyring.asc", filepath.Join(d, "php-8.5.12.tar.xz"),
		filepath.Join("testdata", "php-8.4.27.tar.xz.asc"), lockFprs(t, "8.4")); reason != "bad-signature" {
		t.Errorf("8.4.27's signature over 8.5.12: %q, want bad-signature", reason)
	}
}
