package shadnet

import (
	"errors"
	"io"
	"os"
)

// Load reads backend-owned trust configuration and fetches public keys from the
// configured endpoint. Neither account credentials nor issuer files are shared.
func Load(path string) (*Verifier, error) {
	raw, err := readBounded(path, 16384)
	if err != nil {
		return nil, errors.New("shadNet auth configuration unavailable")
	}
	var config struct {
		Policy
		JWKSURL string `json:"jwks_url"`
	}
	if !decodeObject(raw, &config, "issuer", "client_id", "np_title_id", "scopes", "jwks_url") {
		return nil, errors.New("invalid shadNet auth configuration")
	}
	return newRemoteVerifier(config.Policy, config.JWKSURL, nil)
}

func readBounded(path string, maximum int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maximum {
		return nil, errors.New("invalid configuration size")
	}
	return raw, nil
}
