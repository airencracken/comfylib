// SPDX-License-Identifier: AGPL-3.0-or-later

package token

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"regexp"
	"strings"
	"testing"
	"testing/quick"
)

var hexToken = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestNewReturnsTheTokenAndItsDigest(t *testing.T) {
	token, hash := New()
	if !hexToken.MatchString(token) || hash != Hash(token) || hash == token {
		t.Fatalf("New() = %q, %q", token, hash)
	}
	// Known answer, computed independently with Python's hashlib.
	if got := Hash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("Hash is not hex SHA-256: %s", got)
	}
}

// Both apps already store digests of tokens in this exact format; a change here
// would sign everybody out and void every outstanding link.
func TestTokensKeepTheStoredFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		token, hash := New()
		if !hexToken.MatchString(token) || !hexToken.MatchString(hash) {
			t.Fatalf("format changed: %q %q", token, hash)
		}
		if seen[token] {
			t.Fatal("New repeated a token")
		}
		seen[token] = true
	}
}

func TestEqual(t *testing.T) {
	token, _ := New()
	flipped := []byte(token)
	flipped[len(flipped)-1] ^= 1
	if !Equal(token, token) || !Equal("", "") {
		t.Fatal("equal strings compared unequal")
	}
	for _, other := range []string{"", string(flipped), token + "x", token[:63], strings.ToUpper(token), " " + token} {
		if Equal(token, other) || Equal(other, token) {
			t.Errorf("%q compared equal to %q", other, token)
		}
	}
}

func TestEqualAgreesWithStringEquality(t *testing.T) {
	property := func(a, b string) bool { return Equal(a, b) == (a == b) && Equal(a, a) }
	if err := quick.Check(property, &quick.Config{MaxCount: 2000}); err != nil {
		t.Fatal(err)
	}
}

func TestTokensRoundTripThroughTheirDigest(t *testing.T) {
	property := func(seed uint8) bool {
		token, hash := New()
		generated := NewPrefixed("s" + strings.Repeat("x", int(seed%4)))
		scheme := strings.SplitN(generated.Full, "_", 2)[0]
		prefix, ok := SplitPrefixed(scheme, generated.Full)
		return Hash(token) == hash && Equal(Hash(token), hash) &&
			ok && prefix == generated.Prefix && VerifyPrefixed(generated.Full, generated.Hash)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 500}); err != nil {
		t.Fatal(err)
	}
}

func TestPrefixedTokensRoundTrip(t *testing.T) {
	generated := NewPrefixed("test")
	parts := strings.Split(generated.Full, "_")
	if len(parts) != 3 || parts[0] != "test" || parts[1] != generated.Prefix {
		t.Fatalf("unexpected shape %q", generated.Full)
	}
	if !regexp.MustCompile(`^test_[0-9a-z]{12}_[0-9a-z]{32}$`).MatchString(generated.Full) {
		t.Fatalf("unexpected alphabet or lengths %q", generated.Full)
	}
	if generated.Hash != Hash(generated.Full) {
		t.Fatal("the stored hash is not the digest of the whole token")
	}
	prefix, ok := SplitPrefixed("test", generated.Full)
	if !ok || prefix != generated.Prefix {
		t.Fatalf("SplitPrefixed = %q, %v", prefix, ok)
	}
	if !VerifyPrefixed(generated.Full, generated.Hash) {
		t.Fatal("a fresh token did not verify")
	}
}

// A biased alphabet would make some prefixes more likely than others. Taking
// each byte modulo 36 without rejection makes four symbols 12.5% more likely;
// with 640,000 draws every symbol must land within 6% of its fair share, which
// an unbiased generator misses by chance far less than once in a billion runs.
func TestPrefixedCharactersAreUniform(t *testing.T) {
	counts := map[rune]int{}
	total := 0
	for range 20000 {
		for _, r := range randomBase36(SecretLen) {
			counts[r]++
			total++
		}
	}
	if len(counts) != len(alphabet) {
		t.Fatalf("saw %d distinct characters, want %d", len(counts), len(alphabet))
	}
	fair := total / len(alphabet)
	for r, n := range counts {
		if n < fair*94/100 || n > fair*106/100 {
			t.Errorf("%q appeared %d times; a fair share is about %d", r, n, fair)
		}
	}
}

func TestSplitPrefixedRejectsAnythingElse(t *testing.T) {
	valid := NewPrefixed("test").Full
	prefix, secret := strings.Split(valid, "_")[1], strings.Split(valid, "_")[2]
	for _, bad := range []string{
		"",
		"test",
		"test__",
		"other_" + prefix + "_" + secret,
		"TEST_" + prefix + "_" + secret,
		"test_" + prefix[1:] + "_" + secret,
		"test_" + prefix + "_" + secret + "x",
		"test_" + prefix + "_" + secret + "_extra",
		"test_" + strings.ToUpper(prefix) + "_" + secret,
		"test_" + prefix[:11] + "-_" + secret,
		"test_" + prefix + "_" + secret[:31] + "\x00",
		"test_" + prefix + "_" + secret[:30] + "é",
		" " + valid,
		valid + "\n",
	} {
		if _, ok := SplitPrefixed("test", bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestVerifyPrefixedRejectsNearMisses(t *testing.T) {
	generated := NewPrefixed("test")
	flipped := []byte(generated.Full)
	flipped[len(flipped)-1] ^= 1
	for _, presented := range []string{"", string(flipped), generated.Full + "x", strings.ToUpper(generated.Full)} {
		if VerifyPrefixed(presented, generated.Hash) {
			t.Errorf("%q verified", presented)
		}
	}
	if VerifyPrefixed(generated.Full, "") || VerifyPrefixed(generated.Full, generated.Full) {
		t.Fatal("a token verified against something that is not its digest")
	}
}

// A comparison that returns at the first differing byte tells an attacker
// how much of a guess was right. Timing tests are too noisy to catch that, so
// this reads the source instead: every equality test in the comparison
// functions must be on the result of a constant-time primitive.
func TestComparisonsAreConstantTime(t *testing.T) {
	file, err := parser.ParseFile(gotoken.NewFileSet(), "token.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "Equal" && fn.Name.Name != "VerifyPrefixed") {
			continue
		}
		checked[fn.Name.Name] = true
		if !constantTimeOnly(t, fn) {
			t.Errorf("%s does not use subtle.ConstantTimeCompare", fn.Name.Name)
		}
	}
	if !checked["Equal"] || !checked["VerifyPrefixed"] {
		t.Fatalf("comparison functions not found: %v", checked)
	}
}

// constantTimeOnly reports each comparison in fn that is not on the result of
// subtle.ConstantTimeCompare, and whether fn calls it at all.
func constantTimeOnly(t *testing.T, fn *ast.FuncDecl) bool {
	t.Helper()
	constantTime := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.BinaryExpr:
			if (node.Op == gotoken.EQL || node.Op == gotoken.NEQ) && !isConstantTimeCall(node.X) && !isConstantTimeCall(node.Y) {
				t.Errorf("%s compares with %s outside crypto/subtle", fn.Name.Name, node.Op)
			}
		case *ast.CallExpr:
			if isConstantTimeCall(node) {
				constantTime++
			}
			if selector, ok := node.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := selector.X.(*ast.Ident); ok && (pkg.Name == "bytes" || pkg.Name == "strings") {
					t.Errorf("%s calls %s.%s", fn.Name.Name, pkg.Name, selector.Sel.Name)
				}
			}
		}
		return true
	})
	return constantTime > 0
}

func isConstantTimeCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "subtle" && selector.Sel.Name == "ConstantTimeCompare"
}

func FuzzSplitPrefixed(f *testing.F) {
	f.Add(NewPrefixed("test").Full)
	f.Add("test_a_b")
	f.Add("test_" + strings.Repeat("A", PrefixLen) + "_" + strings.Repeat("a", SecretLen))
	f.Fuzz(func(t *testing.T, full string) {
		prefix, ok := SplitPrefixed("test", full)
		if !ok {
			return
		}
		if len(prefix) != PrefixLen || !strings.HasPrefix(full, "test_"+prefix+"_") || len(full) != len("test_")+PrefixLen+1+SecretLen {
			t.Fatalf("accepted a malformed token %q", full)
		}
		if strings.Trim(full[len("test_"):], alphabet+"_") != "" {
			t.Fatalf("accepted characters outside the alphabet: %q", full)
		}
	})
}
