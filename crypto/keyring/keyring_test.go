package keyring

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/cosmos/go-bip39"
	"github.com/stretchr/testify/require"
	tmcrypto "github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/crypto/multisig"
	"github.com/tendermint/tendermint/crypto/secp256k1"

	"github.com/cosmos/cosmos-sdk/crypto"
	"github.com/cosmos/cosmos-sdk/crypto/keys/hd"
	"github.com/cosmos/cosmos-sdk/tests"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	someKey = "theKey"
	theID   = "theID"
	otherID = "otherID"
)

func init() {
	crypto.BcryptSecurityParameter = 1
}

func TestNewKeyring(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	mockIn := strings.NewReader("")
	t.Cleanup(cleanup)
	kr, err := New("cosmos", BackendFile, dir, mockIn)
	require.NoError(t, err)

	mockIn.Reset("password\npassword\n")
	info, _, err := kr.NewMnemonic("foo", English, AltSecp256k1)
	require.NoError(t, err)
	require.Equal(t, "foo", info.GetName())
}

func TestKeyManagementKeyRing(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	t.Cleanup(cleanup)
	kb, err := New("keybasename", "test", dir, nil)
	require.NoError(t, err)

	algo := AltSecp256k1
	n1, n2, n3 := "personal", "business", "other"

	// Check empty state
	l, err := kb.List()
	require.Nil(t, err)
	require.Empty(t, l)

	_, _, err = kb.NewMnemonic(n1, English, notSupportedAlgo{})
	require.Error(t, err, "ed25519 keys are currently not supported by keybase")

	// create some keys
	_, err = kb.Key(n1)
	require.Error(t, err)
	i, _, err := kb.NewMnemonic(n1, English, algo)

	require.NoError(t, err)
	require.Equal(t, n1, i.GetName())
	_, _, err = kb.NewMnemonic(n2, English, algo)
	require.NoError(t, err)

	// we can get these keys
	i2, err := kb.Key(n2)
	require.NoError(t, err)
	_, err = kb.Key(n3)
	require.NotNil(t, err)
	_, err = kb.KeyByAddress(accAddr(i2))
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32("cosmos1yq8lgssgxlx9smjhes6ryjasmqmd3ts2559g0t")
	require.NoError(t, err)
	_, err = kb.KeyByAddress(addr)
	require.NotNil(t, err)

	// list shows them in order
	keyS, err := kb.List()
	require.NoError(t, err)
	require.Equal(t, 2, len(keyS))
	// note these are in alphabetical order
	require.Equal(t, n2, keyS[0].GetName())
	require.Equal(t, n1, keyS[1].GetName())
	require.Equal(t, i2.GetPubKey(), keyS[0].GetPubKey())

	// deleting a key removes it
	err = kb.Delete("bad name")
	require.NotNil(t, err)
	err = kb.Delete(n1)
	require.NoError(t, err)
	keyS, err = kb.List()
	require.NoError(t, err)
	require.Equal(t, 1, len(keyS))
	_, err = kb.Key(n1)
	require.Error(t, err)

	// create an offline key
	o1 := "offline"
	priv1 := ed25519.GenPrivKey()
	pub1 := priv1.PubKey()
	i, err = kb.SavePubKey(o1, pub1, Ed25519)
	require.Nil(t, err)
	require.Equal(t, pub1, i.GetPubKey())
	require.Equal(t, o1, i.GetName())
	keyS, err = kb.List()
	require.NoError(t, err)
	require.Equal(t, 2, len(keyS))

	// delete the offline key
	err = kb.Delete(o1)
	require.NoError(t, err)
	keyS, err = kb.List()
	require.NoError(t, err)
	require.Equal(t, 1, len(keyS))

	// addr cache gets nuked - and test skip flag
	require.NoError(t, kb.Delete(n2))
}

// TestSignVerify does some detailed checks on how we sign and validate
// signatures
func TestSignVerifyKeyRingWithLedger(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	t.Cleanup(cleanup)
	kb, err := New("keybasename", "test", dir, nil)
	require.NoError(t, err)

	i1, err := kb.SaveLedgerKey("key", AltSecp256k1, "cosmos", 0, 0)
	if err != nil {
		require.Equal(t, "ledger nano S: support for ledger devices is not available in this executable", err.Error())
		t.Skip("ledger nano S: support for ledger devices is not available in this executable")
		return
	}
	require.Equal(t, "key", i1.GetName())

	d1 := []byte("my first message")
	s1, pub1, err := kb.Sign("key", d1)
	require.NoError(t, err)

	s2, pub2, err := SignWithLedger(i1, d1)
	require.NoError(t, err)

	require.Equal(t, i1.GetPubKey(), pub1)
	require.Equal(t, i1.GetPubKey(), pub2)
	require.True(t, pub1.VerifyBytes(d1, s1))
	require.True(t, i1.GetPubKey().VerifyBytes(d1, s1))
	require.True(t, bytes.Equal(s1, s2))

	localInfo, _, err := kb.NewMnemonic("test", English, AltSecp256k1)
	require.NoError(t, err)
	_, _, err = SignWithLedger(localInfo, d1)
	require.Error(t, err)
	require.Equal(t, "not a ledger object", err.Error())
}

func TestSignVerifyKeyRing(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	t.Cleanup(cleanup)

	kb, err := New("keybasename", "test", dir, nil)
	require.NoError(t, err)
	algo := AltSecp256k1

	n1, n2, n3 := "some dude", "a dudette", "dude-ish"

	// create two users and get their info
	i1, _, err := kb.NewMnemonic(n1, English, algo)
	require.Nil(t, err)

	i2, _, err := kb.NewMnemonic(n2, English, algo)
	require.Nil(t, err)

	// Import a public key
	armor, err := kb.ExportPubKeyArmor(n2)
	require.Nil(t, err)
	err = kb.ImportPubKey(n3, armor)
	require.NoError(t, err)
	i3, err := kb.Key(n3)
	require.NoError(t, err)
	require.Equal(t, i3.GetName(), n3)

	// let's try to sign some messages
	d1 := []byte("my first message")
	d2 := []byte("some other important info!")
	d3 := []byte("feels like I forgot something...")

	// try signing both data with both ..
	s11, pub1, err := kb.Sign(n1, d1)
	require.Nil(t, err)
	require.Equal(t, i1.GetPubKey(), pub1)

	s12, pub1, err := kb.Sign(n1, d2)
	require.Nil(t, err)
	require.Equal(t, i1.GetPubKey(), pub1)

	s21, pub2, err := kb.Sign(n2, d1)
	require.Nil(t, err)
	require.Equal(t, i2.GetPubKey(), pub2)

	s22, pub2, err := kb.Sign(n2, d2)
	require.Nil(t, err)
	require.Equal(t, i2.GetPubKey(), pub2)

	// let's try to validate and make sure it only works when everything is proper
	cases := []struct {
		key   tmcrypto.PubKey
		data  []byte
		sig   []byte
		valid bool
	}{
		// proper matches
		{i1.GetPubKey(), d1, s11, true},
		// change data, pubkey, or signature leads to fail
		{i1.GetPubKey(), d2, s11, false},
		{i2.GetPubKey(), d1, s11, false},
		{i1.GetPubKey(), d1, s21, false},
		// make sure other successes
		{i1.GetPubKey(), d2, s12, true},
		{i2.GetPubKey(), d1, s21, true},
		{i2.GetPubKey(), d2, s22, true},
	}

	for i, tc := range cases {
		valid := tc.key.VerifyBytes(tc.data, tc.sig)
		require.Equal(t, tc.valid, valid, "%d", i)
	}

	// Now try to sign data with a secret-less key
	_, _, err = kb.Sign(n3, d3)
	require.Error(t, err)
	require.Equal(t, "cannot sign with offline keys", err.Error())
}

func TestExportImportKeyRing(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	t.Cleanup(cleanup)
	kb, err := New("keybasename", "test", dir, nil)
	require.NoError(t, err)

	info, _, err := kb.NewMnemonic("john", English, AltSecp256k1)
	require.NoError(t, err)
	require.Equal(t, info.GetName(), "john")

	john, err := kb.Key("john")
	require.NoError(t, err)
	require.Equal(t, info.GetName(), "john")
	johnAddr := info.GetPubKey().Address()

	armor, err := kb.ExportPrivKeyArmor("john", "apassphrase")
	require.NoError(t, err)

	err = kb.ImportPrivKey("john2", armor, "apassphrase")
	require.NoError(t, err)

	john2, err := kb.Key("john2")
	require.NoError(t, err)

	require.Equal(t, john.GetPubKey().Address(), johnAddr)
	require.Equal(t, john.GetName(), "john")
	require.Equal(t, john.GetAddress(), john2.GetAddress())
	require.Equal(t, john.GetAlgo(), john2.GetAlgo())
	require.Equal(t, john.GetPubKey(), john2.GetPubKey())
	require.Equal(t, john.GetType(), john2.GetType())
}

func TestExportImportPubKeyKeyRing(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	t.Cleanup(cleanup)
	kb, err := New("keybasename", "test", dir, nil)
	require.NoError(t, err)
	algo := AltSecp256k1

	// CreateMnemonic a private-public key pair and ensure consistency
	info, _, err := kb.NewMnemonic("john", English, algo)
	require.Nil(t, err)
	require.NotEqual(t, info, "")
	require.Equal(t, info.GetName(), "john")
	addr := info.GetPubKey().Address()
	john, err := kb.Key("john")
	require.NoError(t, err)
	require.Equal(t, john.GetName(), "john")
	require.Equal(t, john.GetPubKey().Address(), addr)

	// Export the public key only
	armor, err := kb.ExportPubKeyArmor("john")
	require.NoError(t, err)
	// Import it under a different name
	err = kb.ImportPubKey("john-pubkey-only", armor)
	require.NoError(t, err)
	// Ensure consistency
	john2, err := kb.Key("john-pubkey-only")
	require.NoError(t, err)
	// Compare the public keys
	require.True(t, john.GetPubKey().Equals(john2.GetPubKey()))
	// Ensure the original key hasn't changed
	john, err = kb.Key("john")
	require.NoError(t, err)
	require.Equal(t, john.GetPubKey().Address(), addr)
	require.Equal(t, john.GetName(), "john")

	// Ensure keys cannot be overwritten
	err = kb.ImportPubKey("john-pubkey-only", armor)
	require.NotNil(t, err)
}

func TestAdvancedKeyManagementKeyRing(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	t.Cleanup(cleanup)
	kb, err := New("keybasename", "test", dir, nil)
	require.NoError(t, err)

	algo := AltSecp256k1
	n1, n2 := "old-name", "new name"

	// make sure key works with initial password
	_, _, err = kb.NewMnemonic(n1, English, algo)
	require.Nil(t, err, "%+v", err)

	_, err = kb.ExportPubKeyArmor(n1 + ".notreal")
	require.NotNil(t, err)
	_, err = kb.ExportPubKeyArmor(" " + n1)
	require.NotNil(t, err)
	_, err = kb.ExportPubKeyArmor(n1 + " ")
	require.NotNil(t, err)
	_, err = kb.ExportPubKeyArmor("")
	require.NotNil(t, err)
	exported, err := kb.ExportPubKeyArmor(n1)
	require.Nil(t, err, "%+v", err)

	// import succeeds
	err = kb.ImportPubKey(n2, exported)
	require.NoError(t, err)

	// second import fails
	err = kb.ImportPubKey(n2, exported)
	require.NotNil(t, err)
}

func TestSeedPhraseKeyRing(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	t.Cleanup(cleanup)
	kb, err := New("keybasename", "test", dir, nil)
	require.NoError(t, err)

	algo := AltSecp256k1
	n1, n2 := "lost-key", "found-again"

	// make sure key works with initial password
	info, mnemonic, err := kb.NewMnemonic(n1, English, algo)
	require.Nil(t, err, "%+v", err)
	require.Equal(t, n1, info.GetName())
	require.NotEmpty(t, mnemonic)

	// now, let us delete this key
	err = kb.Delete(n1)
	require.Nil(t, err, "%+v", err)
	_, err = kb.Key(n1)
	require.NotNil(t, err)

	// let us re-create it from the mnemonic-phrase
	params := *hd.NewFundraiserParams(0, sdk.CoinType, 0)
	hdPath := params.String()
	newInfo, err := kb.NewAccount(n2, mnemonic, DefaultBIP39Passphrase, hdPath, AltSecp256k1)
	require.NoError(t, err)
	require.Equal(t, n2, newInfo.GetName())
	require.Equal(t, info.GetPubKey().Address(), newInfo.GetPubKey().Address())
	require.Equal(t, info.GetPubKey(), newInfo.GetPubKey())
}

func TestKeyringKeybaseExportImportPrivKey(t *testing.T) {
	dir, cleanup := tests.NewTestCaseDir(t)
	t.Cleanup(cleanup)
	kb, err := New("keybasename", "test", dir, nil)
	require.NoError(t, err)

	_, _, err = kb.NewMnemonic("john", English, AltSecp256k1)
	require.NoError(t, err)

	keystr, err := kb.ExportPrivKeyArmor("john", "somepassword")
	require.NoError(t, err)
	require.NotEmpty(t, keystr)

	// try import the key - wrong password
	err = kb.ImportPrivKey("john2", keystr, "bad pass")
	require.Equal(t, "failed to decrypt private key: ciphertext decryption failed", err.Error())

	// try import the key with the correct password
	require.NoError(t, kb.ImportPrivKey("john2", keystr, "somepassword"))

	// overwrite is not allowed
	err = kb.ImportPrivKey("john2", keystr, "password")
	require.Equal(t, "cannot overwrite key: john2", err.Error())

	// try export non existing key
	_, err = kb.ExportPrivKeyArmor("john3", "wrongpassword")
	require.Equal(t, "The specified item could not be found in the keyring", err.Error())
}

func TestInMemoryLanguage(t *testing.T) {
	kb := NewInMemory()
	_, _, err := kb.NewMnemonic("something", Japanese, AltSecp256k1)
	require.Error(t, err)
	require.Equal(t, "unsupported language: only english is supported", err.Error())
}

func TestInMemoryCreateMultisig(t *testing.T) {
	kb, err := New("keybasename", "memory", "", nil)
	require.NoError(t, err)
	multi := multisig.PubKeyMultisigThreshold{
		K:       1,
		PubKeys: []tmcrypto.PubKey{secp256k1.GenPrivKey().PubKey()},
	}
	_, err = kb.SaveMultisig("multi", multi)
	require.NoError(t, err)
}

func TestInMemoryCreateAccountInvalidMnemonic(t *testing.T) {
	kb := NewInMemory()
	_, err := kb.NewAccount(
		"some_account",
		"malarkey pair crucial catch public canyon evil outer stage ten gym tornado",
		"", CreateHDPath(0, 0).String(), AltSecp256k1)
	require.Error(t, err)
	require.Equal(t, "Invalid mnemonic", err.Error())
}

func TestInMemoryCreateLedger(t *testing.T) {
	kb := NewInMemory()

	ledger, err := kb.SaveLedgerKey("some_account", AltSecp256k1, "cosmos", 3, 1)

	if err != nil {
		require.Error(t, err)
		require.Equal(t, "ledger nano S: support for ledger devices is not available in this executable", err.Error())
		require.Nil(t, ledger)
		t.Skip("ledger nano S: support for ledger devices is not available in this executable")
		return
	}

	// The mock is available, check that the address is correct
	pubKey := ledger.GetPubKey()
	pk, err := sdk.Bech32ifyPubKey(sdk.Bech32PubKeyTypeAccPub, pubKey)
	require.NoError(t, err)
	require.Equal(t, "cosmospub1addwnpepqdszcr95mrqqs8lw099aa9h8h906zmet22pmwe9vquzcgvnm93eqygufdlv", pk)

	// Check that restoring the key gets the same results
	restoredKey, err := kb.Key("some_account")
	require.NoError(t, err)
	require.NotNil(t, restoredKey)
	require.Equal(t, "some_account", restoredKey.GetName())
	require.Equal(t, TypeLedger, restoredKey.GetType())
	pubKey = restoredKey.GetPubKey()
	pk, err = sdk.Bech32ifyPubKey(sdk.Bech32PubKeyTypeAccPub, pubKey)
	require.NoError(t, err)
	require.Equal(t, "cosmospub1addwnpepqdszcr95mrqqs8lw099aa9h8h906zmet22pmwe9vquzcgvnm93eqygufdlv", pk)

	path, err := restoredKey.GetPath()
	require.NoError(t, err)
	require.Equal(t, "44'/118'/3'/0/1", path.String())
}

// TestInMemoryKeyManagement makes sure we can manipulate these keys well
func TestInMemoryKeyManagement(t *testing.T) {
	// make the storage with reasonable defaults
	cstore := NewInMemory()

	algo := AltSecp256k1
	n1, n2, n3 := "personal", "business", "other"

	// Check empty state
	l, err := cstore.List()
	require.Nil(t, err)
	require.Empty(t, l)

	_, _, err = cstore.NewMnemonic(n1, English, notSupportedAlgo{})
	require.Error(t, err, "ed25519 keys are currently not supported by keybase")

	// create some keys
	_, err = cstore.Key(n1)
	require.Error(t, err)
	i, _, err := cstore.NewMnemonic(n1, English, algo)

	require.NoError(t, err)
	require.Equal(t, n1, i.GetName())
	_, _, err = cstore.NewMnemonic(n2, English, algo)
	require.NoError(t, err)

	// we can get these keys
	i2, err := cstore.Key(n2)
	require.NoError(t, err)
	_, err = cstore.Key(n3)
	require.NotNil(t, err)
	_, err = cstore.KeyByAddress(accAddr(i2))
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32("cosmos1yq8lgssgxlx9smjhes6ryjasmqmd3ts2559g0t")
	require.NoError(t, err)
	_, err = cstore.KeyByAddress(addr)
	require.NotNil(t, err)

	// list shows them in order
	keyS, err := cstore.List()
	require.NoError(t, err)
	require.Equal(t, 2, len(keyS))
	// note these are in alphabetical order
	require.Equal(t, n2, keyS[0].GetName())
	require.Equal(t, n1, keyS[1].GetName())
	require.Equal(t, i2.GetPubKey(), keyS[0].GetPubKey())

	// deleting a key removes it
	err = cstore.Delete("bad name")
	require.NotNil(t, err)
	err = cstore.Delete(n1)
	require.NoError(t, err)
	keyS, err = cstore.List()
	require.NoError(t, err)
	require.Equal(t, 1, len(keyS))
	_, err = cstore.Key(n1)
	require.Error(t, err)

	// create an offline key
	o1 := "offline"
	priv1 := ed25519.GenPrivKey()
	pub1 := priv1.PubKey()
	i, err = cstore.SavePubKey(o1, pub1, Ed25519)
	require.Nil(t, err)
	require.Equal(t, pub1, i.GetPubKey())
	require.Equal(t, o1, i.GetName())
	iOffline := i.(*offlineInfo)
	require.Equal(t, Ed25519, iOffline.GetAlgo())
	keyS, err = cstore.List()
	require.NoError(t, err)
	require.Equal(t, 2, len(keyS))

	// delete the offline key
	err = cstore.Delete(o1)
	require.NoError(t, err)
	keyS, err = cstore.List()
	require.NoError(t, err)
	require.Equal(t, 1, len(keyS))

	// addr cache gets nuked - and test skip flag
	err = cstore.Delete(n2)
	require.NoError(t, err)
}

// TestInMemorySignVerify does some detailed checks on how we sign and validate
// signatures
func TestInMemorySignVerify(t *testing.T) {
	cstore := NewInMemory()
	algo := AltSecp256k1

	n1, n2, n3 := "some dude", "a dudette", "dude-ish"

	// create two users and get their info
	i1, _, err := cstore.NewMnemonic(n1, English, algo)
	require.Nil(t, err)

	i2, _, err := cstore.NewMnemonic(n2, English, algo)
	require.Nil(t, err)

	// Import a public key
	armor, err := cstore.ExportPubKeyArmor(n2)
	require.Nil(t, err)
	err = cstore.ImportPubKey(n3, armor)
	require.NoError(t, err)
	i3, err := cstore.Key(n3)
	require.NoError(t, err)
	require.Equal(t, i3.GetName(), n3)

	// let's try to sign some messages
	d1 := []byte("my first message")
	d2 := []byte("some other important info!")
	d3 := []byte("feels like I forgot something...")

	// try signing both data with both ..
	s11, pub1, err := cstore.Sign(n1, d1)
	require.Nil(t, err)
	require.Equal(t, i1.GetPubKey(), pub1)

	s12, pub1, err := cstore.Sign(n1, d2)
	require.Nil(t, err)
	require.Equal(t, i1.GetPubKey(), pub1)

	s21, pub2, err := cstore.Sign(n2, d1)
	require.Nil(t, err)
	require.Equal(t, i2.GetPubKey(), pub2)

	s22, pub2, err := cstore.Sign(n2, d2)
	require.Nil(t, err)
	require.Equal(t, i2.GetPubKey(), pub2)

	// let's try to validate and make sure it only works when everything is proper
	cases := []struct {
		key   tmcrypto.PubKey
		data  []byte
		sig   []byte
		valid bool
	}{
		// proper matches
		{i1.GetPubKey(), d1, s11, true},
		// change data, pubkey, or signature leads to fail
		{i1.GetPubKey(), d2, s11, false},
		{i2.GetPubKey(), d1, s11, false},
		{i1.GetPubKey(), d1, s21, false},
		// make sure other successes
		{i1.GetPubKey(), d2, s12, true},
		{i2.GetPubKey(), d1, s21, true},
		{i2.GetPubKey(), d2, s22, true},
	}

	for i, tc := range cases {
		valid := tc.key.VerifyBytes(tc.data, tc.sig)
		require.Equal(t, tc.valid, valid, "%d", i)
	}

	// Now try to sign data with a secret-less key
	_, _, err = cstore.Sign(n3, d3)
	require.Error(t, err)
	require.Equal(t, "cannot sign with offline keys", err.Error())
}

// TestInMemoryExportImport tests exporting and importing
func TestInMemoryExportImport(t *testing.T) {
	// make the storage with reasonable defaults
	cstore := NewInMemory()

	info, _, err := cstore.NewMnemonic("john", English, AltSecp256k1)
	require.NoError(t, err)
	require.Equal(t, info.GetName(), "john")

	john, err := cstore.Key("john")
	require.NoError(t, err)
	require.Equal(t, info.GetName(), "john")
	johnAddr := info.GetPubKey().Address()

	armor, err := cstore.ExportPubKeyArmor("john")
	require.NoError(t, err)

	err = cstore.ImportPubKey("john2", armor)
	require.NoError(t, err)

	john2, err := cstore.Key("john2")
	require.NoError(t, err)

	require.Equal(t, john.GetPubKey().Address(), johnAddr)
	require.Equal(t, john.GetName(), "john")
	require.Equal(t, john.GetAddress(), john2.GetAddress())
	require.Equal(t, john.GetAlgo(), john2.GetAlgo())
	require.Equal(t, john.GetPubKey(), john2.GetPubKey())
}

func TestInMemoryExportImportPrivKey(t *testing.T) {
	kb := NewInMemory()

	info, _, err := kb.NewMnemonic("john", English, AltSecp256k1)
	require.NoError(t, err)
	require.Equal(t, info.GetName(), "john")
	priv1, err := kb.Key("john")
	require.NoError(t, err)

	armored, err := kb.ExportPrivKeyArmor("john", "secretcpw")
	require.NoError(t, err)

	// delete exported key
	require.NoError(t, kb.Delete("john"))
	_, err = kb.Key("john")
	require.Error(t, err)

	// import armored key
	require.NoError(t, kb.ImportPrivKey("john", armored, "secretcpw"))

	// ensure old and new keys match
	priv2, err := kb.Key("john")
	require.NoError(t, err)
	require.True(t, priv1.GetPubKey().Equals(priv2.GetPubKey()))
}

func TestInMemoryExportImportPubKey(t *testing.T) {
	// make the storage with reasonable defaults
	cstore := NewInMemory()

	// CreateMnemonic a private-public key pair and ensure consistency
	info, _, err := cstore.NewMnemonic("john", English, AltSecp256k1)
	require.Nil(t, err)
	require.NotEqual(t, info, "")
	require.Equal(t, info.GetName(), "john")
	addr := info.GetPubKey().Address()
	john, err := cstore.Key("john")
	require.NoError(t, err)
	require.Equal(t, john.GetName(), "john")
	require.Equal(t, john.GetPubKey().Address(), addr)

	// Export the public key only
	armor, err := cstore.ExportPubKeyArmor("john")
	require.NoError(t, err)
	// Import it under a different name
	err = cstore.ImportPubKey("john-pubkey-only", armor)
	require.NoError(t, err)
	// Ensure consistency
	john2, err := cstore.Key("john-pubkey-only")
	require.NoError(t, err)
	// Compare the public keys
	require.True(t, john.GetPubKey().Equals(john2.GetPubKey()))
	// Ensure the original key hasn't changed
	john, err = cstore.Key("john")
	require.NoError(t, err)
	require.Equal(t, john.GetPubKey().Address(), addr)
	require.Equal(t, john.GetName(), "john")

	// Ensure keys cannot be overwritten
	err = cstore.ImportPubKey("john-pubkey-only", armor)
	require.NotNil(t, err)
}

// TestInMemoryAdvancedKeyManagement verifies update, import, export functionality
func TestInMemoryAdvancedKeyManagement(t *testing.T) {
	// make the storage with reasonable defaults
	cstore := NewInMemory()

	algo := AltSecp256k1
	n1, n2 := "old-name", "new name"

	// make sure key works with initial password
	_, _, err := cstore.NewMnemonic(n1, English, algo)
	require.Nil(t, err, "%+v", err)

	// exporting requires the proper name and passphrase
	_, err = cstore.ExportPubKeyArmor(n1 + ".notreal")
	require.NotNil(t, err)
	_, err = cstore.ExportPubKeyArmor(" " + n1)
	require.NotNil(t, err)
	_, err = cstore.ExportPubKeyArmor(n1 + " ")
	require.NotNil(t, err)
	_, err = cstore.ExportPubKeyArmor("")
	require.NotNil(t, err)
	exported, err := cstore.ExportPubKeyArmor(n1)
	require.Nil(t, err, "%+v", err)

	// import succeeds
	err = cstore.ImportPubKey(n2, exported)
	require.NoError(t, err)

	// second import fails
	err = cstore.ImportPubKey(n2, exported)
	require.NotNil(t, err)
}

// TestInMemorySeedPhrase verifies restoring from a seed phrase
func TestInMemorySeedPhrase(t *testing.T) {

	// make the storage with reasonable defaults
	cstore := NewInMemory()

	algo := AltSecp256k1
	n1, n2 := "lost-key", "found-again"

	// make sure key works with initial password
	info, mnemonic, err := cstore.NewMnemonic(n1, English, algo)
	require.Nil(t, err, "%+v", err)
	require.Equal(t, n1, info.GetName())
	require.NotEmpty(t, mnemonic)

	// now, let us delete this key
	err = cstore.Delete(n1)
	require.Nil(t, err, "%+v", err)
	_, err = cstore.Key(n1)
	require.NotNil(t, err)

	// let us re-create it from the mnemonic-phrase
	params := *hd.NewFundraiserParams(0, sdk.CoinType, 0)
	hdPath := params.String()
	newInfo, err := cstore.NewAccount(n2, mnemonic, DefaultBIP39Passphrase, hdPath, algo)
	require.NoError(t, err)
	require.Equal(t, n2, newInfo.GetName())
	require.Equal(t, info.GetPubKey().Address(), newInfo.GetPubKey().Address())
	require.Equal(t, info.GetPubKey(), newInfo.GetPubKey())
}

func ExampleNew() {
	// Select the encryption and storage for your cryptostore
	cstore := NewInMemory()

	sec := AltSecp256k1

	// Add keys and see they return in alphabetical order
	bob, _, err := cstore.NewMnemonic("Bob", English, sec)
	if err != nil {
		// this should never happen
		fmt.Println(err)
	} else {
		// return info here just like in List
		fmt.Println(bob.GetName())
	}
	_, _, _ = cstore.NewMnemonic("Alice", English, sec)
	_, _, _ = cstore.NewMnemonic("Carl", English, sec)
	info, _ := cstore.List()
	for _, i := range info {
		fmt.Println(i.GetName())
	}

	// We need to use passphrase to generate a signature
	tx := []byte("deadbeef")
	sig, pub, err := cstore.Sign("Bob", tx)
	if err != nil {
		fmt.Println("don't accept real passphrase")
	}

	// and we can validate the signature with publicly available info
	binfo, _ := cstore.Key("Bob")
	if !binfo.GetPubKey().Equals(bob.GetPubKey()) {
		fmt.Println("Get and Create return different keys")
	}

	if pub.Equals(binfo.GetPubKey()) {
		fmt.Println("signed by Bob")
	}
	if !pub.VerifyBytes(tx, sig) {
		fmt.Println("invalid signature")
	}

	// Output:
	// Bob
	// Alice
	// Bob
	// Carl
	// signed by Bob
}

func TestAltKeyring_List(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	list, err := keyring.List()
	require.NoError(t, err)
	require.Empty(t, list)

	// Fails on creating unsupported pubKeyType
	_, _, err = keyring.NewMnemonic("failing", English, notSupportedAlgo{})
	require.EqualError(t, err, ErrUnsupportedSigningAlgo.Error())

	// Create 3 keys
	uid1, uid2, uid3 := "Zkey", "Bkey", "Rkey"
	_, _, err = keyring.NewMnemonic(uid1, English, AltSecp256k1)
	require.NoError(t, err)
	_, _, err = keyring.NewMnemonic(uid2, English, AltSecp256k1)
	require.NoError(t, err)
	_, _, err = keyring.NewMnemonic(uid3, English, AltSecp256k1)
	require.NoError(t, err)

	list, err = keyring.List()
	require.NoError(t, err)
	require.Len(t, list, 3)

	// Check they are in alphabetical order
	require.Equal(t, uid2, list[0].GetName())
	require.Equal(t, uid3, list[1].GetName())
	require.Equal(t, uid1, list[2].GetName())
}

func TestAltKeyring_NewAccount(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	entropy, err := bip39.NewEntropy(defaultEntropySize)
	require.NoError(t, err)

	mnemonic, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)

	uid := "newUid"

	// Fails on creating unsupported pubKeyType
	_, err = keyring.NewAccount(uid, mnemonic, DefaultBIP39Passphrase, sdk.GetConfig().GetFullFundraiserPath(), notSupportedAlgo{})
	require.EqualError(t, err, ErrUnsupportedSigningAlgo.Error())

	info, err := keyring.NewAccount(uid, mnemonic, DefaultBIP39Passphrase, sdk.GetConfig().GetFullFundraiserPath(), AltSecp256k1)
	require.NoError(t, err)

	require.Equal(t, uid, info.GetName())

	list, err := keyring.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestAltKeyring_SaveLedgerKey(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	// Test unsupported Algo
	_, err = keyring.SaveLedgerKey("key", notSupportedAlgo{}, "cosmos", 0, 0)
	require.EqualError(t, err, ErrUnsupportedSigningAlgo.Error())

	ledger, err := keyring.SaveLedgerKey("some_account", AltSecp256k1, "cosmos", 3, 1)
	if err != nil {
		require.Equal(t, "ledger nano S: support for ledger devices is not available in this executable", err.Error())
		t.Skip("ledger nano S: support for ledger devices is not available in this executable")
		return
	}
	// The mock is available, check that the address is correct
	require.Equal(t, "some_account", ledger.GetName())
	pubKey := ledger.GetPubKey()
	pk, err := sdk.Bech32ifyPubKey(sdk.Bech32PubKeyTypeAccPub, pubKey)
	require.NoError(t, err)
	require.Equal(t, "cosmospub1addwnpepqdszcr95mrqqs8lw099aa9h8h906zmet22pmwe9vquzcgvnm93eqygufdlv", pk)

	// Check that restoring the key gets the same results
	restoredKey, err := keyring.Key("some_account")
	require.NoError(t, err)
	require.NotNil(t, restoredKey)
	require.Equal(t, "some_account", restoredKey.GetName())
	require.Equal(t, TypeLedger, restoredKey.GetType())
	pubKey = restoredKey.GetPubKey()
	pk, err = sdk.Bech32ifyPubKey(sdk.Bech32PubKeyTypeAccPub, pubKey)
	require.NoError(t, err)
	require.Equal(t, "cosmospub1addwnpepqdszcr95mrqqs8lw099aa9h8h906zmet22pmwe9vquzcgvnm93eqygufdlv", pk)

	path, err := restoredKey.GetPath()
	require.NoError(t, err)
	require.Equal(t, "44'/118'/3'/0/1", path.String())
}

func TestAltKeyring_Get(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := someKey
	mnemonic, _, err := keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	key, err := keyring.Key(uid)
	require.NoError(t, err)
	requireEqualInfo(t, mnemonic, key)
}

func TestAltKeyring_KeyByAddress(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := someKey
	mnemonic, _, err := keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	key, err := keyring.KeyByAddress(mnemonic.GetAddress())
	require.NoError(t, err)
	requireEqualInfo(t, key, mnemonic)
}

func TestAltKeyring_Delete(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := someKey
	_, _, err = keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	list, err := keyring.List()
	require.NoError(t, err)
	require.Len(t, list, 1)

	err = keyring.Delete(uid)
	require.NoError(t, err)

	list, err = keyring.List()
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestAltKeyring_DeleteByAddress(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := someKey
	mnemonic, _, err := keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	list, err := keyring.List()
	require.NoError(t, err)
	require.Len(t, list, 1)

	err = keyring.DeleteByAddress(mnemonic.GetAddress())
	require.NoError(t, err)

	list, err = keyring.List()
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestAltKeyring_SavePubKey(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	list, err := keyring.List()
	require.NoError(t, err)
	require.Empty(t, list)

	key := someKey
	priv := ed25519.GenPrivKey()
	pub := priv.PubKey()

	info, err := keyring.SavePubKey(key, pub, AltSecp256k1.Name())
	require.Nil(t, err)
	require.Equal(t, pub, info.GetPubKey())
	require.Equal(t, key, info.GetName())
	require.Equal(t, AltSecp256k1.Name(), info.GetAlgo())

	list, err = keyring.List()
	require.NoError(t, err)
	require.Equal(t, 1, len(list))
}

func TestAltKeyring_SaveMultisig(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	mnemonic1, _, err := keyring.NewMnemonic("key1", English, AltSecp256k1)
	require.NoError(t, err)
	mnemonic2, _, err := keyring.NewMnemonic("key2", English, AltSecp256k1)
	require.NoError(t, err)

	key := "multi"
	pub := multisig.NewPubKeyMultisigThreshold(2, []tmcrypto.PubKey{mnemonic1.GetPubKey(), mnemonic2.GetPubKey()})

	info, err := keyring.SaveMultisig(key, pub)
	require.Nil(t, err)
	require.Equal(t, pub, info.GetPubKey())
	require.Equal(t, key, info.GetName())

	list, err := keyring.List()
	require.NoError(t, err)
	require.Len(t, list, 3)
}

func TestAltKeyring_Sign(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := "jack"
	_, _, err = keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	msg := []byte("some message")

	sign, key, err := keyring.Sign(uid, msg)
	require.NoError(t, err)

	require.True(t, key.VerifyBytes(msg, sign))
}

func TestAltKeyring_SignByAddress(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := "jack"
	mnemonic, _, err := keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	msg := []byte("some message")

	sign, key, err := keyring.SignByAddress(mnemonic.GetAddress(), msg)
	require.NoError(t, err)

	require.True(t, key.VerifyBytes(msg, sign))
}

func TestAltKeyring_ImportExportPrivKey(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := theID
	_, _, err = keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	passphrase := "somePass"
	armor, err := keyring.ExportPrivKeyArmor(uid, passphrase)
	require.NoError(t, err)

	// Should fail importing private key on existing key.
	err = keyring.ImportPrivKey(uid, armor, passphrase)
	require.EqualError(t, err, fmt.Sprintf("cannot overwrite key: %s", uid))

	newUID := otherID
	// Should fail importing with wrong password
	err = keyring.ImportPrivKey(newUID, armor, "wrongPass")
	require.EqualError(t, err, "failed to decrypt private key: ciphertext decryption failed")

	err = keyring.ImportPrivKey(newUID, armor, passphrase)
	require.NoError(t, err)
}

func TestAltKeyring_ImportExportPrivKey_ByAddress(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := theID
	mnemonic, _, err := keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	passphrase := "somePass"
	armor, err := keyring.ExportPrivKeyArmorByAddress(mnemonic.GetAddress(), passphrase)
	require.NoError(t, err)

	// Should fail importing private key on existing key.
	err = keyring.ImportPrivKey(uid, armor, passphrase)
	require.EqualError(t, err, fmt.Sprintf("cannot overwrite key: %s", uid))

	newUID := otherID
	// Should fail importing with wrong password
	err = keyring.ImportPrivKey(newUID, armor, "wrongPass")
	require.EqualError(t, err, "failed to decrypt private key: ciphertext decryption failed")

	err = keyring.ImportPrivKey(newUID, armor, passphrase)
	require.NoError(t, err)
}

func TestAltKeyring_ImportExportPubKey(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := theID
	_, _, err = keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	armor, err := keyring.ExportPubKeyArmor(uid)
	require.NoError(t, err)

	// Should fail importing private key on existing key.
	err = keyring.ImportPubKey(uid, armor)
	require.EqualError(t, err, fmt.Sprintf("cannot overwrite key: %s", uid))

	newUID := otherID
	err = keyring.ImportPubKey(newUID, armor)
	require.NoError(t, err)
}

func TestAltKeyring_ImportExportPubKey_ByAddress(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	uid := theID
	mnemonic, _, err := keyring.NewMnemonic(uid, English, AltSecp256k1)
	require.NoError(t, err)

	armor, err := keyring.ExportPubKeyArmorByAddress(mnemonic.GetAddress())
	require.NoError(t, err)

	// Should fail importing private key on existing key.
	err = keyring.ImportPubKey(uid, armor)
	require.EqualError(t, err, fmt.Sprintf("cannot overwrite key: %s", uid))

	newUID := otherID
	err = keyring.ImportPubKey(newUID, armor)
	require.NoError(t, err)
}

func TestAltKeyring_ConstructorSupportedAlgos(t *testing.T) {
	dir, clean := tests.NewTestCaseDir(t)
	t.Cleanup(clean)

	keyring, err := New(t.Name(), BackendTest, dir, nil)
	require.NoError(t, err)

	// should fail when using unsupported signing algorythm.
	_, _, err = keyring.NewMnemonic("test", English, notSupportedAlgo{})
	require.EqualError(t, err, "unsupported signing algo")

	// but works with default signing algo.
	_, _, err = keyring.NewMnemonic("test", English, AltSecp256k1)
	require.NoError(t, err)

	// but we can create a new keybase with our provided algos.
	dir2, clean2 := tests.NewTestCaseDir(t)
	t.Cleanup(clean2)

	keyring2, err := New(t.Name(), BackendTest, dir2, nil, func(options *keyringOptions) {
		options.supportedAlgos = SigningAlgoList{
			notSupportedAlgo{},
		}
	})
	require.NoError(t, err)

	// now this new keyring does not fail when signing with provided algo
	_, _, err = keyring2.NewMnemonic("test", English, notSupportedAlgo{})
	require.NoError(t, err)
}

func requireEqualInfo(t *testing.T, key Info, mnemonic Info) {
	require.Equal(t, key.GetName(), mnemonic.GetName())
	require.Equal(t, key.GetAddress(), mnemonic.GetAddress())
	require.Equal(t, key.GetPubKey(), mnemonic.GetPubKey())
	require.Equal(t, key.GetAlgo(), mnemonic.GetAlgo())
	require.Equal(t, key.GetType(), mnemonic.GetType())
}

func accAddr(info Info) sdk.AccAddress { return info.GetAddress() }
