package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"agent-platform/backend/internal/client/hotel"
	"agent-platform/backend/internal/client/train"
	"agent-platform/backend/internal/client/weather"
	"agent-platform/backend/internal/model"
	trip "agent-platform/backend/internal/service/trip"
)

type providerDoer func(*http.Request) (*http.Response, error)

func (f providerDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderContract(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC) }
	for _, domain := range []string{"train", "hotel", "weather"} {
		t.Run(domain, func(t *testing.T) {
			invalid := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if invalid {
					io.WriteString(w, `{"unexpected":true}`)
					return
				}
				switch domain {
				case "train":
					if err := r.ParseForm(); err != nil || r.PostForm.Get("departure_station") != "上海" || r.PostForm.Get("date") != "2026-10-25" {
						t.Error("invalid train request")
					}
					io.WriteString(w, `{"error_code":0,"result":[{"train_no":"G1","departure_station":"上海","arrival_station":"杭州","departure_time":"09:00","arrival_time":"10:00","duration":"01:00","enable_booking":"Y","prices":[{"seat_name":"二等座","price":"123.45","num":"有"}]}]}`)
				case "hotel":
					if r.URL.Query().Get("city_limit") != "true" {
						t.Error("hotel query must be city scoped")
					}
					io.WriteString(w, `{"status":"1","infocode":"10000","pois":[{"id":"poi1","name":"测试酒店","typecode":"100100","cityname":"杭州市","address":"测试路","location":"120,30","business":{"cost":"500"}}]}`)
				case "weather":
					io.WriteString(w, `{"current_condition":[{"temp_C":"22","weatherDesc":[{"value":"晴"}]}],"weather":[]}`)
				}
			}))
			defer srv.Close()
			doer := providerDoer(func(r *http.Request) (*http.Response, error) {
				clone := r.Clone(r.Context())
				u, _ := url.Parse(srv.URL)
				clone.URL.Scheme = u.Scheme
				clone.URL.Host = u.Host
				return srv.Client().Do(clone)
			})
			var client trip.QueryClient
			q := model.TravelQuery{Domain: domain, Origin: "上海", Destination: "杭州", Date: "2026-10-25", City: "杭州"}
			switch domain {
			case "train":
				client = train.New("unit-test-key", doer, now)
			case "hotel":
				client = hotel.New("unit-test-key", doer, now)
			case "weather":
				client = weather.New(doer, now)
				q.Date = ""
			}
			e, err := client.Search(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			if e.Source.Provider == "" || e.Source.FetchedAt.IsZero() {
				t.Fatal("missing evidence source")
			}
			switch domain {
			case "train":
				if len(e.Candidates) != 1 || e.Candidates[0].PriceFen == nil || *e.Candidates[0].PriceFen != 12345 || e.Candidates[0].ArrivalAt == nil {
					t.Fatalf("train facts: %+v", e)
				}
			case "hotel":
				if len(e.Candidates) != 1 || e.Candidates[0].PriceFen != nil || e.Message == "" {
					t.Fatalf("POI cost treated as room price: %+v", e)
				}
			case "weather":
				if len(e.Weather) != 1 || e.Weather[0].TemperatureC == nil || *e.Weather[0].TemperatureC != 22 {
					t.Fatalf("weather facts: %+v", e)
				}
			}
			invalid = true
			bad, err := client.Search(context.Background(), q)
			if err == nil && bad.Status != "error" && bad.Status != "unavailable" {
				t.Fatal("invalid provider payload accepted")
			}
		})
	}
}
