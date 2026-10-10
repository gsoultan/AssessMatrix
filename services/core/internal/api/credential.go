package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
)

// Open Badges 3.0 (1EdTech), secured as a VC-JWT with RS256: the format and
// algorithm every conforming verifier accepts. The credential is public by
// design; it names its recipient only as a salted hash of their email, so it
// can be checked against an address someone already knows and reveals none.

const (
	credContextVC = "https://www.w3.org/ns/credentials/v2"
	credContextOB = "https://purl.imsglobal.org/spec/ob/v3p0/context-3.0.3.json"
)

// IssuerURL is the issuer's id: a URL that serves its profile.
func (s *Server) IssuerURL(tenant string) string { return s.PublicURL + "/issuers/" + tenant }

// CredentialURL is where a person checks a credential.
func (s *Server) CredentialURL(id string) string { return s.PublicURL + "/verify/#" + id }

type issuerKey struct {
	kid  string
	priv *rsa.PrivateKey
}

// signingKey is the tenant's current key, made on first use.
func (s *Server) signingKey(ctx context.Context, tx pgx.Tx) (*issuerKey, error) {
	tenant := string(auth.From(ctx).Tenant)
	var kid string
	var sealed []byte
	err := tx.QueryRow(ctx, `SELECT kid, private_key FROM issuer_keys WHERE retired_at IS NULL`).Scan(&kid, &sealed)
	if err == nil {
		der, err := s.Keys.Open("issuer:"+tenant+":"+kid, sealed)
		if err != nil {
			return nil, fmt.Errorf("open issuer key: %w", err)
		}
		priv, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return nil, err
		}
		rk, ok := priv.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("issuer key is not RSA")
		}
		return &issuerKey{kid: kid, priv: rk}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	pubDER, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	sum := sha256.Sum256(pubDER)
	kid = hex.EncodeToString(sum[:8])
	sealed, err = s.Keys.Seal("issuer:"+tenant+":"+kid, der)
	if err != nil {
		return nil, err
	}
	jwk := map[string]string{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
		"n": b64(priv.N.Bytes()), "e": b64(big.NewInt(int64(priv.E)).Bytes()),
	}
	// A concurrent first issue made one already: use that one.
	tag, err := tx.Exec(ctx, `INSERT INTO issuer_keys (tenant_id, kid, public_jwk, private_key)
		VALUES (am_tenant(), $1, $2, $3) ON CONFLICT DO NOTHING`, kid, jwk, sealed)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return s.signingKey(ctx, tx)
	}
	return &issuerKey{kid: kid, priv: priv}, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func signJWT(k *issuerKey, claims any) (string, error) {
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": k.kid})
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	in := b64(head) + "." + b64(body)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(nil, k.priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return in + "." + b64(sig), nil
}

// settleCredential issues, revokes or restores the attempt's credential to
// match its score: a passed exam with nothing left for a person to score
// earns one. Returns the credential id when one is in force.
func (s *Server) settleCredential(ctx context.Context, tx pgx.Tx, att *attemptRow, e *v1.Exam, score *v1.Score) (string, error) {
	earned := e.GetKind() == v1.Kind_KIND_EXAM && score != nil && score.GetPassed() && score.GetPendingHuman() == 0 && score.GetMax() > 0
	var id string
	var revoked *time.Time
	err := tx.QueryRow(ctx, `SELECT id, revoked_at FROM credentials WHERE attempt_id = $1`, att.ID).Scan(&id, &revoked)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	switch {
	case exists && !earned:
		if revoked == nil {
			_, err = tx.Exec(ctx, `UPDATE credentials SET revoked_at = now(), revoked_why = $2 WHERE id = $1`, id,
				"a later review of the attempt brought the score below the pass mark")
		}
		return "", err
	case exists && earned:
		if revoked != nil {
			_, err = tx.Exec(ctx, `UPDATE credentials SET revoked_at = NULL, revoked_why = NULL WHERE id = $1`, id)
		}
		return id, err
	case !earned:
		return "", nil
	}

	var tenantName, email, title string
	if err := tx.QueryRow(ctx, `SELECT t.name, p.email FROM tenants t, assignments a JOIN people p ON p.id = a.person_id
		WHERE t.id = am_tenant() AND a.id = $1`, att.AssignmentID).Scan(&tenantName, &email); err != nil {
		return "", err
	}
	title = e.GetTitle()
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		return "", err
	}
	key, err := s.signingKey(ctx, tx)
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	saltHex := hex.EncodeToString(salt)
	idHash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email)) + saltHex))
	issuer := s.IssuerURL(string(auth.From(ctx).Tenant))
	credID := "urn:uuid:" + id
	vc := map[string]any{
		"@context":  []string{credContextVC, credContextOB},
		"id":        credID,
		"type":      []string{"VerifiableCredential", "OpenBadgeCredential"},
		"issuer":    map[string]any{"id": issuer, "type": []string{"Profile"}, "name": tenantName},
		"validFrom": now.Format(time.RFC3339),
		"name":      title,
		"credentialSubject": map[string]any{
			"type": []string{"AchievementSubject"},
			"identifier": []map[string]any{{
				"type": "IdentityObject", "identityType": "emailAddress", "hashed": true,
				"identityHash": "sha256$" + hex.EncodeToString(idHash[:]), "salt": saltHex,
			}},
			"achievement": map[string]any{
				"id":          "urn:uuid:" + e.GetId(),
				"type":        []string{"Achievement"},
				"name":        title,
				"description": fmt.Sprintf("Passed %s, an assessment delivered by %s.", title, tenantName),
				"criteria": map[string]any{"narrative": fmt.Sprintf("Scored at least %d%% on %s (version %d).",
					e.GetPassPct(), title, att.ExamVersion)},
				"achievementType": "Assessment",
			},
		},
	}
	claims := map[string]any{}
	for k, v := range vc {
		claims[k] = v
	}
	claims["iss"], claims["jti"], claims["nbf"], claims["iat"] = issuer, credID, now.Unix(), now.Unix()
	jwt, err := signJWT(key, claims)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credentials (id, tenant_id, attempt_id, jwt) VALUES ($1, am_tenant(), $2, $3)`,
		id, att.ID, jwt); err != nil {
		return "", err
	}
	link := s.CredentialURL(id)
	body := fmt.Sprintf("Selamat! Anda lulus %s.\n\nSertifikat digital Anda (Open Badges 3.0) dapat diperiksa siapa saja di:\n%s\n\n"+
		"Bagikan tautan ini kepada siapa pun yang perlu memeriksanya. Sertifikat tidak memuat alamat email Anda secara terbuka.\n\n— %s",
		title, link, tenantName)
	if err := s.queueMail(ctx, tx, "credential", email, "Sertifikat Anda: "+title, body); err != nil {
		return "", err
	}
	return id, nil
}
