// Command geoip-sync downloads DB-IP's free IP-to-City Lite database, which
// GET /api/location/approximate reads. The database is not committed (about
// 130 MB); deploys and fresh clones fetch it:
//
//	cd server && go run ./cmd/geoip-sync
//
// DB-IP publishes a new edition each month. The tool tries the current month
// and falls back to the previous one, since the new edition can appear a few
// days into the month.
//
// The database is licensed CC BY 4.0 and requires attribution
// ("IP Geolocation by DB-IP", https://db-ip.com).
package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

const downloadURLFormat = "https://download.db-ip.com/free/dbip-city-lite-%s.mmdb.gz"

func main() {
	out := flag.String("out", "assets/geoip/dbip-city-lite.mmdb", "output path for the database")
	flag.Parse()

	client := &http.Client{Timeout: 5 * time.Minute}
	now := time.Now().UTC()
	editions := []string{
		now.Format("2006-01"),
		now.AddDate(0, 0, -now.Day()).Format("2006-01"), // last day of previous month
	}

	var lastErr error
	for _, edition := range editions {
		if err := download(client, fmt.Sprintf(downloadURLFormat, edition), *out); err != nil {
			log.Printf("edition %s: %v", edition, err)
			lastErr = err
			continue
		}
		log.Printf("wrote %s (DB-IP City Lite %s)", *out, edition)
		return
	}
	log.Fatalf("download IP database: %v", lastErr)
}

// download writes to a temporary file next to out, checks that it opens as a
// MaxMind DB with city records, and only then renames it into place, so a
// failed download never replaces a working database.
func download(client *http.Client, url, out string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".geoip-*.mmdb")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, gz); err != nil {
		tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	reader, err := maxminddb.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	dbType := reader.Metadata.DatabaseType
	reader.Close()
	if dbType == "" {
		return fmt.Errorf("verify: database has no type")
	}

	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, out)
}
