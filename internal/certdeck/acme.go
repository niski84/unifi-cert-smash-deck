package certdeck

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
)

type acmeUser struct {
	Email        string
	Registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string {
	return u.Email
}

func (u *acmeUser) GetRegistration() *registration.Resource {
	return u.Registration
}

func (u *acmeUser) GetPrivateKey() crypto.PrivateKey {
	return u.key
}

func accountKeyPath() string {
	return filepath.Join(DataDir(), "acme-account.pem")
}

func registrationPath() string {
	return filepath.Join(DataDir(), "acme-registration.json")
}

func loadOrCreateAccountKey() (crypto.PrivateKey, error) {
	p := accountKeyPath()
	raw, err := os.ReadFile(p)
	if err == nil && len(raw) > 0 {
		k, err := certcrypto.ParsePEMPrivateKey(raw)
		if err != nil {
			return nil, err
		}
		return k, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	pemBytes := certcrypto.PEMEncode(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func loadRegistration() *registration.Resource {
	raw, err := os.ReadFile(registrationPath())
	if err != nil {
		return nil
	}
	var reg registration.Resource
	if json.Unmarshal(raw, &reg) != nil {
		return nil
	}
	if reg.URI == "" {
		return nil
	}
	return &reg
}

func saveRegistration(reg *registration.Resource) error {
	if reg == nil {
		return nil
	}
	b, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(registrationPath(), b, 0o644)
}

// ObtainCertificate requests a certificate via DNS-01 (Cloudflare) using lego.
func ObtainCertificate(ctx context.Context, cfg AppConfig) (certPEM []byte, keyPEM []byte, err error) {
	if cfg.Domain == "" {
		return nil, nil, fmt.Errorf("domain is empty")
	}
	if cfg.ACMEEmail == "" {
		return nil, nil, fmt.Errorf("acme_email is empty")
	}
	if cfg.CloudflareAPIToken == "" {
		return nil, nil, fmt.Errorf("cloudflare_api_token is empty")
	}

	key, err := loadOrCreateAccountKey()
	if err != nil {
		return nil, nil, err
	}
	reg := loadRegistration()
	user := &acmeUser{Email: cfg.ACMEEmail, Registration: reg, key: key}

	lcfg := lego.NewConfig(user)
	if cfg.ACMEUseStaging {
		lcfg.CADirURL = lego.LEDirectoryStaging
	} else {
		lcfg.CADirURL = lego.LEDirectoryProduction
	}

	client, err := lego.NewClient(lcfg)
	if err != nil {
		return nil, nil, err
	}

	if user.GetRegistration() == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, nil, fmt.Errorf("acme register: %w", err)
		}
		user.Registration = reg
		if err := saveRegistration(reg); err != nil {
			return nil, nil, err
		}
	}

	dns, err := cloudflare.NewDNSProviderConfig(&cloudflare.Config{
		AuthToken: cfg.CloudflareAPIToken,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("cloudflare dns: %w", err)
	}
	if err := client.Challenge.SetDNS01Provider(dns); err != nil {
		return nil, nil, err
	}

	req := certificate.ObtainRequest{
		Domains: []string{cfg.Domain},
		Bundle:  true,
	}
	// lego uses context from Obtain via client internal - Obtain passes context in v4?
	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return nil, nil, err
	}
	return res.Certificate, res.PrivateKey, nil
}
