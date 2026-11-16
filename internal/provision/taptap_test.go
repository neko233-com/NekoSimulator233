package provision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTapTapDownloadStreamsAndRestrictsRedirects(t *testing.T) {
	payload := []byte("PK\x03\x04payload")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/apk", http.StatusFound)
		case "/unsafe":
			http.Redirect(w, r, "https://other.invalid/app.apk", http.StatusFound)
		case "/downgrade":
			http.Redirect(w, r, "http://127.0.0.1/app.apk", http.StatusFound)
		case "/html":
			_, _ = w.Write([]byte("<html>error</html>"))
		case "/apk":
			_, _ = w.Write(payload)
		case "/large":
			w.(http.Flusher).Flush() // unknown Content-Length
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]bool{serverURL.Host: true}
	var out bytes.Buffer
	result, err := downloadTapTap(context.Background(), &out, server.Client(), server.URL+"/redirect", hosts, 1024)
	if err != nil || !bytes.Equal(out.Bytes(), payload) || result.Bytes != int64(len(payload)) {
		t.Fatalf("download: %+v %v", result, err)
	}
	sum := sha256.Sum256(payload)
	if result.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("incorrect downloaded digest")
	}
	for _, path := range []string{"/unsafe", "/downgrade", "/html", "/missing"} {
		out.Reset()
		if _, err := downloadTapTap(context.Background(), &out, server.Client(), server.URL+path, hosts, 1024); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	out.Reset()
	if _, err := downloadTapTap(context.Background(), &out, server.Client(), server.URL+"/large", hosts, 8); err == nil || out.Len() > 9 {
		t.Fatal("unbounded streaming response accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := downloadTapTap(ctx, &out, server.Client(), server.URL+"/apk", hosts, 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
