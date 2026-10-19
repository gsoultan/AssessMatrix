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

	"github.com/gsoultan/storm/runtime"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/credential"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/issuerkey"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/tenant"
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

// CredentialStatusURL is the credential's revocation list (1EdTech
// Revocation List Status Method): empty while it stands.
func (s *Server) CredentialStatusURL(id string) string {
	return s.PublicURL + "/credentials/" + id + "/status"
}

// CredentialURL is where a person checks a credential.
func (s *Server) CredentialURL(id string) string { return s.PublicURL + "/verify/#" + id }

type issuerKey struct {
	kid  string
	priv *rsa.PrivateKey
}

// signingKey is the tenant's current key, made on first use.
func (s *Server) signingKey(ctx context.Context, tx *db.Tx) (*issuerKey, error) {
	tenant := string(auth.From(ctx).Tenant)
	cur, ok, err := issuerkey.New().Where(issuerkey.RetiredAt.IsNull()).One(ctx, tx)
	if err != nil {
		return nil, err
	}
	if ok {
		der, err := s.Keys.Open("issuer:"+tenant+":"+cur.Kid, cur.PrivateKey)
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
		return &issuerKey{kid: cur.Kid, priv: rk}, nil
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
	kid := hex.EncodeToString(sum[:8])
	sealed, err := s.Keys.Seal("issuer:"+tenant+":"+kid, der)
	if err != nil {
		return nil, err
	}
	jwk := map[string]string{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
		"n": b64(priv.N.Bytes()), "e": b64(big.NewInt(int64(priv.E)).Bytes()),
	}
	pub, err := json.Marshal(jwk)
	if err != nil {
		return nil, err
	}
	n := issuerkey.Create()
	n.SetTenantID(tx.Tenant())
	n.SetKid(kid)
	n.SetPublicJwk(pub)
	n.SetPrivateKey(sealed)
	if _, err := n.DoNothing().Insert(ctx, tx); errors.Is(err, runtime.ErrConflict) {
		// A concurrent first issue made one already: use that one.
		return s.signingKey(ctx, tx)
	} else if err != nil {
		return nil, err
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
// earns one, if its record is intact. A credential vouches for the record, so
// one whose record was altered is never issued, and is revoked if it was.
// Returns the credential id when one is in force.
func (s *Server) settleCredential(ctx context.Context, tx *db.Tx, att *attemptRow, e *v1.Exam, score *v1.Score, intact bool) (string, error) {
	earned := intact && e.GetKind() == v1.Kind_KIND_EXAM && score != nil && score.GetPassed() && score.GetPendingHuman() == 0 && score.GetMax() > 0
	cur, exists, err := credential.New().Where(credential.AttemptID.Eq(key(att.ID))).ForUpdate().One(ctx, tx)
	if err != nil {
		return "", err
	}
	id, revoked := idText(cur.ID), cur.RevokedAt.Valid
	credEvent := func(typ string, extra map[string]any) error {
		d := map[string]any{"credential_id": id, "attempt_id": att.ID, "exam_id": att.ExamID, "person_id": att.PersonID,
			"url": s.CredentialURL(id)}
		for k, v := range extra {
			d[k] = v
		}
		return s.emit(ctx, tx, typ, att.PersonID, d)
	}
	switch {
	case exists && !earned:
		if !revoked {
			why := "a later review of the attempt brought the score below the pass mark"
			if !intact {
				why = "the attempt's stored record no longer matches its seal"
			}
			m := credential.Mutate(cur)
			m.SetRevokedAtNow()
			m.SetRevokedWhy(why)
			if err := m.Update(ctx, tx); err != nil {
				return "", err
			}
			return "", credEvent(EventCredentialRevoked, map[string]any{"reason": why})
		}
		return "", nil
	case exists && earned:
		if revoked {
			m := credential.Mutate(cur)
			m.SetRevokedAtNull()
			m.SetRevokedWhyNull()
			if err := m.Update(ctx, tx); err != nil {
				return "", err
			}
			return id, credEvent(EventCredentialIssued, map[string]any{"restored": true})
		}
		return id, nil
	case !earned:
		return "", nil
	}

	ten, ok, err := tenant.New().IDEq(tx.Tenant()).One(ctx, tx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", notFound("tenant")
	}
	tenantName, title := ten.Name, e.GetTitle()
	email, err := personEmail(ctx, tx, att.PersonID)
	if err != nil {
		return "", err
	}
	uid := newID()
	id = idText(uid)
	signer, err := s.signingKey(ctx, tx)
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
		// Revocation that any verifier can check, not only ours.
		"credentialStatus": map[string]any{"id": s.CredentialStatusURL(id), "type": "1EdTechRevocationList"},
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
	jwt, err := signJWT(signer, claims)
	if err != nil {
		return "", err
	}
	n := credential.Create()
	n.SetID(uid)
	n.SetTenantID(tx.Tenant())
	n.SetAttemptID(key(att.ID))
	n.SetJwt(jwt)
	if _, err := n.Insert(ctx, tx); err != nil {
		return "", err
	}
	if err := credEvent(EventCredentialIssued, map[string]any{"restored": false}); err != nil {
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
