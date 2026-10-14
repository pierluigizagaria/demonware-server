package demonware

// Demonware lobby crypto primitives, reproduced identically to what the
// client does (LibTomCrypt in the NSO, SHA-1 throughout). The offsets below
// are inherited Diablo III reverse-engineering notes, not new PS4 evidence.
//
//	0xBDB5F0  HMACSHA1(20-byte key, data)
//	0xBDB730  hkdfExpandSHA1(key, label, n)  — T1 = HMAC(k, lbl||01),
//	          Ti = HMAC(k, T(i-1)||lbl||i), output truncated to n
//	0xBDC9D0  sha1(data)
//	+0x230/+0x238: AES-128-CBC (0x6B0-byte object, inited with 16 bytes)

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"errors"
)

// HMACSHA1 implements the legacy lobby MAC, not an application signing policy.
func HMACSHA1(key, data []byte) []byte {
	m := hmac.New(sha1.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// hkdfExpandSHA1 follows the loop at 0xBDB730: the first block does not
// prefix an empty T(0), the counter is a byte starting at 1.
func hkdfExpandSHA1(key, label []byte, n int) []byte {
	out := make([]byte, 0, n+sha1.Size)
	var prev []byte
	for i := byte(1); len(out) < n; i++ {
		buf := make([]byte, 0, len(prev)+len(label)+1)
		buf = append(buf, prev...)
		buf = append(buf, label...)
		buf = append(buf, i)
		prev = HMACSHA1(key, buf)
		out = append(out, prev...)
	}
	return out[:n]
}

// SessionKeys is what 0xBFBB80 derives at the end of the handshake.
type SessionKeys struct {
	ClientTag   [8]byte // last 8 bytes of the 0x82 message
	ServerCheck [8]byte // to send back in 0x83 (compared to conn+0x224)
	C2SMAC      []byte  // conn+0x240, 20 bytes
	S2CMAC      []byte  // conn+0x254, 20 bytes
	C2SAES      []byte  // conn+0x230 object, 16 bytes
	S2CAES      []byte  // conn+0x238 object, 16 bytes
}

// DeriveKeys: secret = HMAC-SHA1(key=SHA1(transcript), msg=key24), then
// CLIENTCHAL (16) and BDDATA (72).
func DeriveKeys(transcript, key24 []byte) SessionKeys {
	digest := sha1.Sum(transcript)
	secret := HMACSHA1(digest[:], key24)

	chal := hkdfExpandSHA1(secret, []byte("CLIENTCHAL"), 16)
	data := hkdfExpandSHA1(secret, []byte("BDDATA"), 72)

	var k SessionKeys
	copy(k.ClientTag[:], chal[0:8])
	copy(k.ServerCheck[:], chal[8:16])
	k.C2SMAC = append([]byte{}, data[0:20]...)
	k.S2CMAC = append([]byte{}, data[20:40]...)
	k.C2SAES = append([]byte{}, data[40:56]...)
	k.S2CAES = append([]byte{}, data[56:72]...)
	return k
}

// WrapKey24 is the conn+0x208 flag's variant: the ticket key goes through
// HKDF with the DER public key as the label.
func WrapKey24(key24, publicKeyDER []byte) []byte {
	return hkdfExpandSHA1(key24, publicKeyDER, 24)
}

// DecryptCBC decrypts complete blocks without removing padding or checking a MAC.
func DecryptCBC(key, iv, ct []byte) ([]byte, error) {
	if len(iv) != aes.BlockSize {
		return nil, errors.New("invalid IV length")
	}
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("invalid ciphertext length")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(b, iv).CryptBlocks(pt, ct)
	return pt, nil
}

// EncryptCBC encrypts complete blocks without adding padding or a MAC.
func EncryptCBC(key, iv, pt []byte) ([]byte, error) {
	if len(iv) != aes.BlockSize {
		return nil, errors.New("invalid IV length")
	}
	if len(pt)%aes.BlockSize != 0 {
		return nil, errors.New("plaintext not block-aligned")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ct := make([]byte, len(pt))
	cipher.NewCBCEncrypter(b, iv).CryptBlocks(ct, pt)
	return ct, nil
}
