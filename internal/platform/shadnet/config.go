package shadnet

import "errors"

// Check validates a trust policy and its key endpoint without contacting it,
// reporting every problem.
func Check(policy Policy, jwksURL string) error {
	_, policyErr := newVerifier(policy)
	return errors.Join(policyErr, checkEndpoint(jwksURL))
}

// Load binds the backend's trust policy to public keys fetched from its
// configured endpoint. Neither account credentials nor issuer files are shared.
func Load(policy Policy, jwksURL string) (*Verifier, error) {
	return newRemoteVerifier(policy, jwksURL, nil)
}
