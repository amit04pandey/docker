package plugins

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/pkg/plugins/transport"
	"github.com/docker/go-connections/tlsconfig"
)

var (
	mux    *http.ServeMux
	server *httptest.Server
)

func setupRemotePluginServer() string {
	mux = http.NewServeMux()
	server = httptest.NewServer(mux)
	return server.URL
}

func teardownRemotePluginServer() {
	if server != nil {
		server.Close()
	}
}

func testHTTPTimeout(t *testing.T, timeout, epsilon time.Duration) {
	addr := setupRemotePluginServer()
	defer teardownRemotePluginServer()
	stop := make(chan struct{}) // we need this variable to stop the http server
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		<-stop
	})
	c, _ := NewClient(addr, &tlsconfig.Options{InsecureSkipVerify: true})
	c.http.Timeout = timeout
	begin := time.Now()
	_, err := c.callWithRetry("hang", nil, false)
	close(stop)
	if err == nil || !strings.Contains(err.Error(), "request canceled") {
		t.Fatalf("The request should be canceled %v", err)
	}
	elapsed := time.Now().Sub(begin)
	if elapsed < timeout-epsilon || elapsed > timeout+epsilon {
		t.Fatalf("elapsed time: got %v, expected %v (epsilon=%v)",
			elapsed, timeout, epsilon)
	}
}

func TestHTTPTimeout(t *testing.T) {
	testHTTPTimeout(t, 5*time.Second, 500*time.Millisecond)
}

func TestFailedConnection(t *testing.T) {
	c, _ := NewClient("tcp://127.0.0.1:1", &tlsconfig.Options{InsecureSkipVerify: true})
	_, err := c.callWithRetry("Service.Method", nil, false)
	if err == nil {
		t.Fatal("Unexpected successful connection")
	}
}

func TestEchoInputOutput(t *testing.T) {
	addr := setupRemotePluginServer()
	defer teardownRemotePluginServer()

	m := Manifest{[]string{"VolumeDriver", "NetworkDriver"}}

	mux.HandleFunc("/Test.Echo", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("Expected POST, got %s\n", r.Method)
		}

		header := w.Header()
		header.Set("Content-Type", transport.VersionMimetype)

		io.Copy(w, r.Body)
	})

	c, _ := NewClient(addr, &tlsconfig.Options{InsecureSkipVerify: true})
	var output Manifest
	err := c.Call("Test.Echo", m, &output)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(output, m) {
		t.Fatalf("Expected %v, was %v\n", m, output)
	}
	err = c.Call("Test.Echo", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestBackoff(t *testing.T) {
	cases := []struct {
		retries    int
		expTimeOff time.Duration
	}{
		{0, time.Duration(1)},
		{1, time.Duration(2)},
		{2, time.Duration(4)},
		{4, time.Duration(16)},
		{6, time.Duration(30)},
		{10, time.Duration(30)},
	}

	for _, c := range cases {
		s := c.expTimeOff * time.Second
		if d := backoff(c.retries); d != s {
			t.Fatalf("Retry %v, expected %v, was %v\n", c.retries, s, d)
		}
	}
}

func TestAbortRetry(t *testing.T) {
	cases := []struct {
		timeOff  time.Duration
		expAbort bool
	}{
		{time.Duration(1), false},
		{time.Duration(2), false},
		{time.Duration(10), false},
		{time.Duration(30), true},
		{time.Duration(40), true},
	}

	for _, c := range cases {
		s := c.timeOff * time.Second
		if a := abort(time.Now(), s); a != c.expAbort {
			t.Fatalf("Duration %v, expected %v, was %v\n", c.timeOff, s, a)
		}
	}
}

func TestClientScheme(t *testing.T) {
	cases := map[string]string{
		"tcp://127.0.0.1:8080":          "http",
		"unix:///usr/local/plugins/foo": "http",
		"http://127.0.0.1:8080":         "http",
		"https://127.0.0.1:8080":        "https",
	}

	for addr, scheme := range cases {
		u, err := url.Parse(addr)
		if err != nil {
			t.Fatal(err)
		}
		s := httpScheme(u)

		if s != scheme {
			t.Fatalf("URL scheme mismatch, expected %s, got %s", scheme, s)
		}
	}
}
