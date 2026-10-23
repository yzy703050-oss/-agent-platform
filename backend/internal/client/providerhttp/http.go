package providerhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

var ErrResponse = errors.New("invalid provider response")
var ErrRequest = errors.New("provider request failed")

func Default(client Doer) Doer {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func Decode(ctx context.Context, client Doer, req *http.Request, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrRequest
	}
	if resp == nil || resp.Body == nil {
		return ErrResponse
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider HTTP status %d", resp.StatusCode)
	}
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return ErrResponse
	}
	if json.Unmarshal(data, out) != nil {
		return ErrResponse
	}
	return nil
}
