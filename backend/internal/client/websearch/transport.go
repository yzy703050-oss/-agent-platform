package websearch

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type boundedTransport struct{ inner http.RoundTripper }

func (t *boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if req.URL.String() != "https://html.duckduckgo.com/html/" || req.Method != http.MethodPost {
		return nil, ErrUnavailable
	}
	response, err := t.inner.RoundTrip(req)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrUnavailable
	}
	if err = req.Context().Err(); err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnavailable
	}
	if doc.Find("#challenge-form, #anomaly-form, .anomaly-modal").Length() > 0 {
		return nil, ErrUnavailable
	}
	// A parser miss is not evidence of a genuine empty search. Recognize known pages.
	results := doc.Find("#links .web-result h2.result__title > a").Length() > 0
	empty := doc.Find("#links .no-results, #links .result--no-result").Length() > 0
	if !results && !empty {
		return nil, ErrUnavailable
	}
	if !results && !strings.Contains(strings.ToLower(doc.Text()), "no results") {
		return nil, ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}
