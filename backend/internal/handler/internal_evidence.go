package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"agent-platform/backend/internal/evidence"
	"agent-platform/backend/internal/model"
)

type ServiceCredential struct{ ServiceID, Token string }
type evidenceCredential struct {
	service string
	digest  [32]byte
}
type internalEvidence struct {
	store       evidence.Exporter
	credentials []evidenceCredential
}

func NewInternalEvidence(store evidence.Exporter, credentials []ServiceCredential) (http.Handler, error) {
	if store == nil || len(credentials) == 0 {
		return nil, model.ErrInvalidInput
	}
	h := &internalEvidence{store: store}
	seen := map[[32]byte]bool{}
	for _, c := range credentials {
		if (c.ServiceID != "platform" && c.ServiceID != "travel") || c.Token == "" || strings.ContainsAny(c.Token, "\r\n") {
			return nil, model.ErrInvalidInput
		}
		sum := sha256.Sum256([]byte(c.Token))
		if seen[sum] {
			return nil, model.ErrConflict
		}
		seen[sum] = true
		h.credentials = append(h.credentials, evidenceCredential{c.ServiceID, sum})
	}
	return h, nil
}
func (h *internalEvidence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer := ""
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		sum := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
		for _, c := range h.credentials {
			if subtle.ConstantTimeCompare(sum[:], c.digest[:]) == 1 {
				peer = c.service
			}
		}
	}
	if peer == "" {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method unavailable", 405)
		return
	}
	path := strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/")
	if len(path) != 6 || path[0] != "internal" || path[1] != "v1" || path[2] != "evidence" || path[4] != "versions" || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	id, err := url.PathUnescape(path[3])
	if err != nil {
		http.Error(w, "invalid identity", 400)
		return
	}
	revision, err := strconv.ParseInt(path[5], 10, 64)
	if err != nil || revision < 1 {
		http.Error(w, "invalid revision", 400)
		return
	}
	raw, err := h.store.ExportEvidence(r.Context(), evidence.PeerScope{ScopeID: r.Header.Get("X-Execution-Scope-ID"), ScopeHash: r.Header.Get("X-Execution-Scope-Hash"), PeerService: peer}, evidence.Locator{OriginID: id, OriginRevision: revision, ContentHash: r.Header.Get("X-Evidence-Content-Hash")})
	if err != nil {
		http.Error(w, "evidence unavailable", 403)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Evidence-Content-Hash", r.Header.Get("X-Evidence-Content-Hash"))
	w.Write(raw)
}
