package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeIPLocator struct {
	location approximateLocation
	ok       bool
	err      error
	seen     []string
}

func (f *fakeIPLocator) Locate(ip net.IP) (approximateLocation, bool, error) {
	f.seen = append(f.seen, ip.String())
	return f.location, f.ok, f.err
}

func serveApproximate(t *testing.T, handler *ApproximateLocationHandler, flyClientIP string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := newEngine()
	r.GET("/api/location/approximate", handler.approximate)
	req := httptest.NewRequest(http.MethodGet, "/api/location/approximate", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	if flyClientIP != "" {
		req.Header.Set("Fly-Client-IP", flyClientIP)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

var dublin = approximateLocation{City: "Dublin", CountryCode: "IE", Latitude: 53.35, Longitude: -6.26, Source: "ip"}

func TestApproximateLocationReturnsCityForPublicIP(t *testing.T) {
	locator := &fakeIPLocator{location: dublin, ok: true}
	rec := serveApproximate(t, &ApproximateLocationHandler{locator: locator}, "87.32.0.1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got approximateLocation
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != dublin {
		t.Fatalf("body = %+v, want %+v", got, dublin)
	}
	if len(locator.seen) != 1 || locator.seen[0] != "87.32.0.1" {
		t.Fatalf("looked up %v, want the Fly client IP", locator.seen)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Fatalf("Cache-Control = %q", cc)
	}
}

func TestApproximateLocationNoContentCases(t *testing.T) {
	cases := []struct {
		name    string
		handler *ApproximateLocationHandler
		ip      string
	}{
		{"no database", &ApproximateLocationHandler{}, "87.32.0.1"},
		{"private address", &ApproximateLocationHandler{locator: &fakeIPLocator{location: dublin, ok: true}}, "192.168.1.5"},
		{"loopback", &ApproximateLocationHandler{locator: &fakeIPLocator{location: dublin, ok: true}}, "127.0.0.1"},
		{"not found", &ApproximateLocationHandler{locator: &fakeIPLocator{}}, "87.32.0.1"},
		{"lookup error", &ApproximateLocationHandler{locator: &fakeIPLocator{err: errors.New("boom")}}, "87.32.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveApproximate(t, tc.handler, tc.ip)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("body = %q, want empty", rec.Body.String())
			}
		})
	}
}

func TestApproximateLocationDevIPReplacesOnlyPrivateAddresses(t *testing.T) {
	locator := &fakeIPLocator{location: dublin, ok: true}
	handler := &ApproximateLocationHandler{locator: locator, devIP: net.ParseIP("87.32.0.1")}

	if rec := serveApproximate(t, handler, "10.0.2.2"); rec.Code != http.StatusOK {
		t.Fatalf("private address with dev IP: status = %d, want 200", rec.Code)
	}
	if rec := serveApproximate(t, handler, "2a02:8084::1"); rec.Code != http.StatusOK {
		t.Fatalf("public IPv6: status = %d, want 200", rec.Code)
	}
	want := []string{"87.32.0.1", "2a02:8084::1"}
	if len(locator.seen) != 2 || locator.seen[0] != want[0] || locator.seen[1] != want[1] {
		t.Fatalf("looked up %v, want %v", locator.seen, want)
	}
}

func TestRoundCoordinate(t *testing.T) {
	if got := roundCoordinate(53.34981); got != 53.35 {
		t.Fatalf("roundCoordinate = %v, want 53.35", got)
	}
	if got := roundCoordinate(-6.26031); got != -6.26 {
		t.Fatalf("roundCoordinate = %v, want -6.26", got)
	}
}

// TestMMDBLocatorAgainstDownloadedDatabase runs only where cmd/geoip-sync has
// fetched the real database; CI without it skips.
func TestMMDBLocatorAgainstDownloadedDatabase(t *testing.T) {
	if _, err := os.Stat(defaultGeoIPDatabasePath); err != nil {
		t.Skip("no IP database; run go run ./cmd/geoip-sync")
	}
	locator, err := openMMDBLocator(defaultGeoIPDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	// 8.8.8.8 is stable and always resolves to a US city.
	location, ok, err := locator.Locate(net.ParseIP("8.8.8.8"))
	if err != nil || !ok {
		t.Fatalf("Locate(8.8.8.8) = %+v, %v, %v", location, ok, err)
	}
	if location.CountryCode != "US" || location.City == "" || location.Source != "ip" {
		t.Fatalf("unexpected location %+v", location)
	}
	if _, ok, _ := locator.Locate(net.ParseIP("10.0.0.1")); ok {
		t.Fatal("a private address must not resolve")
	}
}
