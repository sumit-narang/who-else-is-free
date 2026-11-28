package main

import (
	"errors"
	"log"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/oschwald/maxminddb-golang"
)

// GET /api/location/approximate answers with the city the caller's IP address
// points to, so Discover can show plans nearby when the person has not shared
// their device location. The lookup runs against a local IP-to-city database
// (DB-IP Lite, fetched at deploy time by cmd/geoip-sync); the address never
// leaves the server and nothing about the lookup is stored.
//
// It answers 204 No Content whenever there is nothing useful to say: no
// database on this server, a private address, or an address without a city.

const defaultGeoIPDatabasePath = "assets/geoip/dbip-city-lite.mmdb"

// approximateCoordinateDecimals rounds the city centre to roughly 1 km. The
// database only knows city centroids anyway; rounding keeps the response from
// looking more precise than it is.
const approximateCoordinateDecimals = 2

type approximateLocation struct {
	City        string  `json:"city"`
	CountryCode string  `json:"country_code"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Source      string  `json:"source"`
}

// ipLocator resolves an IP address to a city. ok is false when the address is
// not in the database or has no city.
type ipLocator interface {
	Locate(ip net.IP) (approximateLocation, bool, error)
}

// mmdbCityRecord is the subset of the DB-IP / GeoLite2 City schema we read.
type mmdbCityRecord struct {
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Location struct {
		Latitude  *float64 `maxminddb:"latitude"`
		Longitude *float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
}

type mmdbLocator struct {
	reader *maxminddb.Reader
}

func openMMDBLocator(path string) (*mmdbLocator, error) {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &mmdbLocator{reader: reader}, nil
}

func (l *mmdbLocator) Locate(ip net.IP) (approximateLocation, bool, error) {
	var record mmdbCityRecord
	if err := l.reader.Lookup(ip, &record); err != nil {
		return approximateLocation{}, false, err
	}
	city := strings.TrimSpace(record.City.Names["en"])
	if city == "" || record.Location.Latitude == nil || record.Location.Longitude == nil {
		return approximateLocation{}, false, nil
	}
	return approximateLocation{
		City:        city,
		CountryCode: strings.ToUpper(strings.TrimSpace(record.Country.ISOCode)),
		Latitude:    roundCoordinate(*record.Location.Latitude),
		Longitude:   roundCoordinate(*record.Location.Longitude),
		Source:      "ip",
	}, true, nil
}

func roundCoordinate(value float64) float64 {
	scale := math.Pow(10, approximateCoordinateDecimals)
	return math.Round(value*scale) / scale
}

type ApproximateLocationHandler struct {
	locator ipLocator
	// devIP stands in for private client addresses (an emulator talking to a
	// local server through 10.0.2.2), so the feature can be exercised locally.
	// Public addresses, which is all production ever sees, ignore it.
	devIP net.IP
}

// NewApproximateLocationHandler opens the IP database named by GEOIP_DB_PATH
// (default assets/geoip/dbip-city-lite.mmdb). A missing database is not an
// error: the endpoint then answers 204 and the app falls back to showing all
// plans, exactly as it does without any location.
func NewApproximateLocationHandler() *ApproximateLocationHandler {
	path := strings.TrimSpace(os.Getenv("GEOIP_DB_PATH"))
	if path == "" {
		path = filepath.Clean(defaultGeoIPDatabasePath)
	}

	handler := &ApproximateLocationHandler{
		devIP: net.ParseIP(strings.TrimSpace(os.Getenv("APPROXIMATE_LOCATION_DEV_IP"))),
	}
	locator, err := openMMDBLocator(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("approximate location disabled: no IP database at %s (run: go run ./cmd/geoip-sync)", path)
		} else {
			log.Printf("approximate location disabled: open %s: %v", path, err)
		}
		return handler
	}
	handler.locator = locator
	return handler
}

func (h *ApproximateLocationHandler) approximate(c *gin.Context) {
	// The answer depends on the caller's network, so only the caller may cache it.
	c.Header("Cache-Control", "private, max-age=3600")
	c.Header("Vary", "Fly-Client-IP")

	if h.locator == nil {
		c.Status(http.StatusNoContent)
		return
	}

	ip := h.lookupIP(c.ClientIP())
	if ip == nil {
		c.Status(http.StatusNoContent)
		return
	}

	location, ok, err := h.locator.Locate(ip)
	if err != nil {
		// Never log the address itself.
		log.Printf("approximate location lookup failed: %v", err)
		c.Status(http.StatusNoContent)
		return
	}
	if !ok {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, location)
}

// lookupIP returns the address to look up, or nil when there is none worth
// looking up. Private, loopback and link-local addresses are swapped for the
// dev override when one is configured.
func (h *ApproximateLocationHandler) lookupIP(raw string) net.IP {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	addr = addr.Unmap()
	if isPublicAddr(addr) {
		return net.IP(addr.AsSlice())
	}
	return h.devIP
}

func isPublicAddr(addr netip.Addr) bool {
	return addr.IsValid() &&
		addr.IsGlobalUnicast() &&
		!addr.IsPrivate() &&
		!addr.IsLoopback() &&
		!addr.IsLinkLocalUnicast()
}
