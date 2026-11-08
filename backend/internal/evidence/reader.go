package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"agent-platform/backend/internal/client/providerhttp"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeprotocol"
)

type Locator struct {
	OriginService  string `json:"origin_service"`
	OriginID       string `json:"origin_id"`
	OriginRevision int64  `json:"origin_revision"`
	ContentHash    string `json:"content_hash"`
}
type View struct {
	ReferenceID    string
	ResultRevision int64
	Record         runtimev1.SharedToolEvidenceRecord
}
type PeerScope struct{ ScopeID, ScopeHash, PeerService string }
type Store interface {
	LocalOrImport(context.Context, runtimev1.ExecRef, string, int64) (View, *Locator, error)
	SaveImport(context.Context, runtimev1.ExecRef, Locator, string, json.RawMessage) (View, error)
}
type Exporter interface {
	ExportEvidence(context.Context, PeerScope, Locator) (json.RawMessage, error)
}
type Remote struct{ ServiceID, Slug, Endpoint, BearerToken string }
type Reader struct {
	store   Store
	remotes map[string]Remote
	http    *http.Client
}

var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ContentHash(raw []byte) (string, error) {
	canonical, err := runtimeprotocol.CanonicalJSON(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
func RemoteID(slug, id string) (string, error) {
	if !slugPattern.MatchString(slug) || id == "" || !utf8.ValidString(id) {
		return "", model.ErrInvalidInput
	}
	sum := sha256.Sum256([]byte(id))
	return "remote:" + slug + ":" + hex.EncodeToString(sum[:]), nil
}
func ValidLocator(l Locator) bool {
	return l.OriginService != "" && l.OriginID != "" && utf8.ValidString(l.OriginID) && l.OriginRevision > 0 && hashPattern.MatchString(l.ContentHash)
}
func ManifestAllows(raw json.RawMessage, l Locator) bool {
	var manifest struct {
		Evidence []Locator `json:"evidence"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return false
	}
	for _, v := range manifest.Evidence {
		if v == l {
			return true
		}
	}
	return false
}
func Decode(raw []byte, l Locator) (runtimev1.SharedToolEvidenceRecord, error) {
	var record runtimev1.SharedToolEvidenceRecord
	hash, err := ContentHash(raw)
	if err != nil || !ValidLocator(l) || hash != l.ContentHash {
		return record, model.ErrConflict
	}
	if err = runtimeprotocol.DecodeStrict(raw, "SharedToolEvidenceRecord", &record); err != nil {
		return record, err
	}
	if record.ID != l.OriginID || record.ResultRevision != l.OriginRevision {
		return record, model.ErrConflict
	}
	return record, nil
}

func NewReader(store Store, remotes []Remote, timeout time.Duration) (*Reader, error) {
	if store == nil || timeout <= 0 {
		return nil, model.ErrInvalidInput
	}
	reader := &Reader{store: store, remotes: map[string]Remote{}, http: providerhttp.SingleHTTPClient(&http.Client{Timeout: timeout})}
	slugs := map[string]bool{}
	for _, remote := range remotes {
		u, err := url.Parse(remote.Endpoint)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback())) || remote.ServiceID == "" || !slugPattern.MatchString(remote.Slug) || remote.BearerToken == "" || strings.ContainsAny(remote.BearerToken, "\r\n") || slugs[remote.Slug] {
			return nil, model.ErrInvalidInput
		}
		if _, exists := reader.remotes[remote.ServiceID]; exists {
			return nil, model.ErrConflict
		}
		slugs[remote.Slug] = true
		reader.remotes[remote.ServiceID] = remote
	}
	return reader, nil
}

func (r *Reader) Resolve(ctx context.Context, ref runtimev1.ExecRef, id string, revision int64) (View, error) {
	view, locator, err := r.store.LocalOrImport(ctx, ref, id, revision)
	if err != nil {
		return View{}, err
	}
	if locator == nil {
		return view, nil
	}
	return r.Import(ctx, ref, *locator)
}
func (r *Reader) Import(ctx context.Context, ref runtimev1.ExecRef, l Locator) (View, error) {
	remote, ok := r.remotes[l.OriginService]
	if !ok || !ValidLocator(l) {
		return View{}, model.ErrInvalidInput
	}
	id, err := RemoteID(remote.Slug, l.OriginID)
	if err != nil {
		return View{}, err
	}
	// A preflight rejects stale leases and unauthorized cached identities before
	// contacting another service. SaveImport repeats the same check after I/O.
	if _, err = r.store.SaveImport(ctx, ref, l, id, nil); err != nil {
		return View{}, fmt.Errorf("evidence preflight: %w", err)
	}
	endpoint := strings.TrimRight(remote.Endpoint, "/") + "/internal/v1/evidence/" + url.PathEscape(l.OriginID) + "/versions/" + strconv.FormatInt(l.OriginRevision, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return View{}, err
	}
	req.Header.Set("Authorization", "Bearer "+remote.BearerToken)
	req.Header.Set("X-Execution-Scope-ID", ref.ScopeID)
	req.Header.Set("X-Execution-Scope-Hash", ref.ScopeHash)
	req.Header.Set("X-Evidence-Content-Hash", l.ContentHash)
	response, err := r.http.Do(req)
	if err != nil {
		return View{}, fmt.Errorf("origin evidence unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return View{}, fmt.Errorf("origin evidence HTTP status %d: %w", response.StatusCode, model.ErrConflict)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1000001))
	if err != nil || len(raw) > 1000000 {
		return View{}, model.ErrInvalidInput
	}
	if response.Header.Get("X-Evidence-Content-Hash") != l.ContentHash {
		return View{}, model.ErrConflict
	}
	if _, err = Decode(raw, l); err != nil {
		return View{}, err
	}
	return r.store.SaveImport(ctx, ref, l, id, raw)
}
